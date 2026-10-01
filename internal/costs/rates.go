package costs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const (
	ratesMaxAge  = 7 * 24 * time.Hour // refetch the official card after this
	ratesRetry   = 24 * time.Hour     // but never try more often than this
	fetchTimeout = 5 * time.Second
)

// sourceOrder ranks where a rate came from: the first source with a matching
// rate decides, and the longest prefix decides within a source; see matchesRate.
var sourceOrder = []string{"override", "official", "calibrated", "builtin"}

// rateFields are the keys of a rate in the overrides and the official cache,
// in the order of the ledger's token columns.
var rateFields = []string{"input", "output", "cache_write_5m", "cache_write_1h", "cache_read"}

func (r Rate) field(name string) float64 {
	return [...]float64{r.Input, r.Output, r.CacheWrite5m, r.CacheWrite1h, r.CacheRead}[slices.Index(rateFields, name)]
}

func (r *Rate) set(name string, v float64) {
	*[...]*float64{&r.Input, &r.Output, &r.CacheWrite5m, &r.CacheWrite1h, &r.CacheRead}[slices.Index(rateFields, name)] = v
}

// matchesRate reports whether the rate keyed prefix prices model: both carry
// the same service tier (none for standard), and the prefix is the whole model
// id or is followed by "-" or "@" and a digit (a version within a family, or a
// dated snapshot). So "claude-opus" prices "claude-opus-5-5" and "gpt-5"
// prices "gpt-5-2025-08-07", but "gpt-5" prices neither "gpt-5-mini" nor
// "gpt-5.3-codex-spark", and no standard rate prices a "@fast" model.
func matchesRate(model, prefix string) bool {
	modelBase, modelTier := SplitTier(model)
	prefixBase, prefixTier := SplitTier(prefix)
	if modelTier != prefixTier || !strings.HasPrefix(modelBase, prefixBase) {
		return false
	}
	rest := modelBase[len(prefixBase):]
	return rest == "" ||
		(len(rest) >= 2 && (rest[0] == '-' || rest[0] == '@') && rest[1] >= '0' && rest[1] <= '9')
}

// Lookup returns the rate that prices model.
func (c RateCard) Lookup(model string) (Rate, bool) {
	var best Rate
	bestKey, found := [2]int{}, false
	for prefix, rate := range c {
		if !matchesRate(model, prefix) {
			continue
		}
		key := [2]int{slices.Index(sourceOrder, rate.Source), -len(prefix)}
		if !found || key[0] < bestKey[0] || (key[0] == bestKey[0] && key[1] < bestKey[1]) {
			best, bestKey, found = rate, key, true
		}
	}
	return best, found
}

// Cost is the list-price cost in USD of the tokens at this rate.
func (r Rate) Cost(t Tokens) float64 {
	return (float64(t.Input)*r.Input + float64(t.Output)*r.Output +
		float64(t.CacheWrite5m)*r.CacheWrite5m + float64(t.CacheWrite1h)*r.CacheWrite1h +
		float64(t.CacheRead)*r.CacheRead) / 1e6
}

// Card is the merged rate card of every recorded tool.
type Card struct {
	Rows         RateCard
	Official     map[string]RateCard  // per tool: the cached official rows
	Calibrated   RateCard             // per model, from the tools' own cost records
	Fetched      map[string]time.Time // per tool: when the official card was fetched
	Overrides    string               // path of the manual overrides file
	HasOverrides bool
	Notes        []string // outcomes of the refresh this call made
}

// Sources are the distinct sources the card's rows come from, sorted.
func (c Card) Sources() []string {
	seen := map[string]bool{}
	for _, rate := range c.Rows {
		seen[rate.Source] = true
	}
	out := make([]string, 0, len(seen))
	for source := range seen {
		out = append(out, source)
	}
	sort.Strings(out)
	return out
}

// Disagreements are the models whose calibrated and official input or output
// rates differ by more than five percent.
func (c Card) Disagreements() map[string]bool {
	out := map[string]bool{}
	for model, calibrated := range c.Calibrated {
		for _, official := range c.Official {
			if o, ok := longestPrefix(model, official); ok &&
				(math.Abs(calibrated.Input-o.Input) > 0.05*math.Max(o.Input, 1e-9) ||
					math.Abs(calibrated.Output-o.Output) > 0.05*math.Max(o.Output, 1e-9)) {
				out[model] = true
			}
		}
	}
	return out
}

func longestPrefix(model string, card RateCard) (Rate, bool) {
	best, found := "", false
	for prefix := range card {
		if matchesRate(model, prefix) && (!found || len(prefix) > len(best)) {
			best, found = prefix, true
		}
	}
	return card[best], found
}

// Rates returns the merged card: built-in, then calibration, then the official
// page, then the manual overrides. refresh refetches every official card first
// and reports each outcome in Notes. A partial override completes itself from
// the prefix-matched rate beneath it.
func Rates(ctx context.Context, refresh bool) (Card, error) {
	paths, err := Locations()
	if err != nil {
		return Card{}, err
	}
	var notes []string
	if refresh {
		for _, tool := range Tools {
			if tool.Source != nil && tool.Source.PricingURL() != "" {
				notes = append(notes, refreshOfficial(ctx, paths, tool, true))
			}
		}
	}
	card, err := loadCard(paths)
	card.Notes = notes
	return card, err
}

func loadCard(paths Paths) (Card, error) {
	card := Card{
		Rows:      RateCard{},
		Official:  map[string]RateCard{},
		Fetched:   map[string]time.Time{},
		Overrides: paths.Overrides,
	}
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		maps.Copy(card.Rows, tool.Source.Builtin())
	}
	card.Calibrated = calibrated(paths.Home)
	maps.Copy(card.Rows, card.Calibrated)
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		official := readOfficial(paths, tool)
		card.Official[tool.Name] = official.rates()
		if official.FetchedAt > 0 {
			card.Fetched[tool.Name] = time.Unix(int64(official.FetchedAt), 0)
		}
		maps.Copy(card.Rows, card.Official[tool.Name])
	}
	overrides, err := readOverrides(paths.Overrides)
	if err != nil {
		return card, err
	}
	card.HasOverrides = len(overrides) > 0
	for _, prefix := range slices.Sorted(maps.Keys(overrides)) {
		base, _ := card.Rows.Lookup(prefix)
		for field, value := range overrides[prefix] {
			base.set(field, value)
		}
		base.Source = "override"
		card.Rows[prefix] = base
	}
	return card, nil
}

// readOverrides reads the manual overrides file: {model prefix: {rate field:
// USD per million tokens}}. A key outside the rate fields or a non-numeric
// value rejects the whole file; nothing is converted or silently ignored.
func readOverrides(path string) (map[string]map[string]float64, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	invalid := func(format string, args ...any) error {
		return operation.Fail(
			operation.ExitInvalid,
			"rates",
			path+": "+fmt.Sprintf(format, args...),
		)
	}
	if err != nil {
		return nil, invalid("%v", err)
	}
	var user map[string]any
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, invalid("top level must be an object of model prefixes (%v)", err)
	}
	out := map[string]map[string]float64{}
	for prefix, value := range user {
		fields, ok := value.(map[string]any)
		if !ok || len(fields) == 0 {
			return nil, invalid("%q must map rate fields to numbers", prefix)
		}
		out[prefix] = map[string]float64{}
		for name, v := range fields {
			if !slices.Contains(rateFields, name) {
				hint := ""
				if name == "cache_write" {
					hint = " (cache_write was split into cache_write_5m and cache_write_1h)"
				}
				return nil, invalid(
					"%q has unknown field %q%s; fields are %s. Edit the file or move it aside.",
					prefix, name, hint, strings.Join(rateFields, ", "),
				)
			}
			number, ok := v.(float64)
			if !ok {
				return nil, invalid("%q.%s must be a number", prefix, name)
			}
			out[prefix][name] = number
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- official card

// officialFile is the cached official card. Its keys are those of the file the
// Python script wrote, so the cache carries over.
type officialFile struct {
	FetchedAt    float64                       `json:"fetched_at"`
	CheckedAt    float64                       `json:"checked_at"`
	ETag         string                        `json:"etag"`
	LastModified string                        `json:"last_modified"`
	URL          string                        `json:"url"`
	Rates        map[string]map[string]float64 `json:"rates"`
}

// rates are the cached rows that carry all five fields.
func (f officialFile) rates() RateCard {
	card := RateCard{}
	for model, fields := range f.Rates {
		var rate Rate
		complete := true
		for _, name := range rateFields {
			v, ok := fields[name]
			complete = complete && ok
			rate.set(name, v)
		}
		if complete {
			rate.Source = "official"
			card[model] = rate
		}
	}
	return card
}

// officialPath is the cache of one tool's official card: rates-official.json
// for the first tool, rates-official-<tool>.json for every other.
func officialPath(paths Paths, tool Tool) string {
	name := "rates-official.json"
	if tool.Name != firstTool {
		name = "rates-official-" + tool.Name + ".json"
	}
	return filepath.Join(filepath.Dir(paths.Ledger), name)
}

func readOfficial(paths Paths, tool Tool) officialFile {
	var file officialFile
	if data, err := os.ReadFile(officialPath(paths, tool)); err == nil {
		_ = json.Unmarshal(data, &file) // a damaged cache is an absent one
	}
	return file
}

func writeOfficial(paths Paths, tool Tool, file officialFile) error {
	path := officialPath(paths, tool)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func epoch(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

// refreshOfficial is a conditional GET of the tool's pricing page. It is best
// effort: any failure keeps the cached card, records the attempt time and
// returns a one-line reason. Without force a fresh enough card is left alone.
func refreshOfficial(ctx context.Context, paths Paths, tool Tool, force bool) string {
	label := "rates"
	if tool.Name != firstTool {
		label += " " + tool.Name
	}
	cached := readOfficial(paths, tool)
	age := time.Since(time.Unix(0, int64(cached.FetchedAt*1e9)))
	if !force && len(cached.rates()) > 0 && age < ratesMaxAge {
		return fmt.Sprintf("%s: cached (%.1fd old)", label, age.Hours()/24)
	}
	cached.CheckedAt = epoch(time.Now())
	keep := func(msg string) string {
		_ = writeOfficial(paths, tool, cached)
		return msg
	}
	url := tool.Source.PricingURL()
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return keep(fmt.Sprintf("%s: fetch failed (%v); keeping cached card", label, err))
	}
	req.Header.Set("User-Agent", "workbench-costs")
	if cached.ETag != "" {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	if cached.LastModified != "" {
		req.Header.Set("If-Modified-Since", cached.LastModified)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return keep(fmt.Sprintf("%s: fetch failed (%v); keeping cached card", label, err))
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotModified && len(cached.rates()) > 0:
		cached.FetchedAt = epoch(time.Now())
		return keep(label + ": official card not modified")
	case resp.StatusCode != http.StatusOK:
		return keep(
			fmt.Sprintf("%s: fetch failed (HTTP %d); keeping cached card", label, resp.StatusCode),
		)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return keep(fmt.Sprintf("%s: fetch failed (%v); keeping cached card", label, err))
	}
	parsed := tool.Source.ParsePricing(string(body))
	if len(parsed) == 0 {
		return keep(label + ": fetch ok but no model rows parsed; keeping cached card")
	}
	file := officialFile{
		FetchedAt:    epoch(time.Now()),
		CheckedAt:    epoch(time.Now()),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		URL:          url,
		Rates:        map[string]map[string]float64{},
	}
	for model, rate := range parsed {
		file.Rates[model] = map[string]float64{}
		for _, name := range rateFields {
			file.Rates[model][name] = rate.field(name)
		}
	}
	if err := writeOfficial(paths, tool, file); err != nil {
		return fmt.Sprintf("%s: fetched but could not save the card (%v)", label, err)
	}
	return fmt.Sprintf("%s: official card updated (%d models)", label, len(parsed))
}

// refreshIfNeeded is the worker's refresh policy: fetch when a tool's official
// card is missing or older than seven days, or when a ledger model has no rate
// anywhere; in all cases at most once a day.
func refreshIfNeeded(ctx context.Context, ledger *Ledger, paths Paths) string {
	card, err := loadCard(paths)
	if err != nil {
		return "rates: overrides file invalid, see `workbench costs rates`"
	}
	var messages []string
	for _, tool := range Tools {
		if tool.Source == nil || tool.Source.PricingURL() == "" {
			continue
		}
		label := "rates"
		if tool.Name != firstTool {
			label += " " + tool.Name
		}
		unpriced := unpricedModels(ledger, card, tool)
		cached := readOfficial(paths, tool)
		age := time.Since(time.Unix(0, int64(cached.FetchedAt*1e9)))
		sinceCheck := time.Since(time.Unix(0, int64(cached.CheckedAt*1e9)))
		var msg string
		switch {
		case len(cached.rates()) > 0 && age < ratesMaxAge && len(unpriced) == 0:
			msg = fmt.Sprintf("%s: cached (%.1fd old)", label, age.Hours()/24)
		case sinceCheck >= ratesRetry:
			msg = refreshOfficial(ctx, paths, tool, true)
		case len(cached.rates()) > 0:
			msg = fmt.Sprintf(
				"%s: cached (%.1fd old), retry in %.0fh",
				label,
				age.Hours()/24,
				(ratesRetry - sinceCheck).Hours(),
			)
		default:
			msg = fmt.Sprintf(
				"%s: no official card, retry in %.0fh",
				label,
				(ratesRetry - sinceCheck).Hours(),
			)
		}
		if len(unpriced) > 0 {
			var names []string
			for _, u := range unpriced {
				names = append(names, fmt.Sprintf("%s (%s)", u.Model, u.Reason))
			}
			msg += "; unpriced: " + strings.Join(names, ", ") + "; add rates to " +
				tilde(paths.Overrides, paths.Home)
		}
		messages = append(messages, msg)
	}
	return strings.Join(messages, "; ")
}

// Unpriced is a model no rate prices, and why. Its tokens count; its cost
// shows as 0 until a rate for it is added to the overrides file.
type Unpriced struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// unpriced is why no rate prices model, one of tool's: the transcript named
// no model, the price page has the model but not at its tier, or the page
// does not have it at all.
func (c Card) unpriced(tool Tool, model string) Unpriced {
	base, tier := SplitTier(model)
	reason := "not on " + tool.PricePage
	switch _, standard := c.Rows.Lookup(base); {
	case base == "unknown":
		reason = "the transcript names no model"
	case tier != "" && standard:
		reason = "no " + tier + " price on " + tool.PricePage
	}
	return Unpriced{Model: model, Reason: reason}
}

// tilde writes a path under home as ~/...
func tilde(path, home string) string {
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok && home != "" {
		return "~/" + rest
	}
	return path
}

// unpricedModels are the tool's ledger models no source prices.
func unpricedModels(ledger *Ledger, card Card, tool Tool) []Unpriced {
	rows, err := ledger.db.Query("SELECT DISTINCT model FROM responses WHERE tool = ?", tool.Name)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []Unpriced
	for rows.Next() {
		var model string
		if rows.Scan(&model) == nil {
			if _, ok := card.Rows.Lookup(model); !ok {
				out = append(out, card.unpriced(tool, model))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

// ---------------------------------------------------------------- calibration

// calibrated solves rates from each tool's own (tokens, cost) pairs: at least
// four observations of a model, a non-negative solution and a residual under
// five percent of the observed cost. A field whose token column carries under
// 0.5% of the mass cannot be solved and keeps the built-in value. The records
// hold one cache-write count, so the 1h rate is derived as 1.6x the solved
// write rate (2.0 / 1.25).
func calibrated(home string) RateCard {
	out := RateCard{}
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		byModel := map[string][]Observation{}
		for _, o := range tool.Source.Observations(home) {
			byModel[o.Model] = append(byModel[o.Model], o)
		}
		builtin := tool.Source.Builtin()
		for model, observed := range byModel {
			if rate, ok := calibrate(model, observed, builtin); ok {
				out[model] = rate
			}
		}
	}
	return out
}

func calibrate(model string, observed []Observation, builtin RateCard) (Rate, bool) {
	if len(observed) < 4 {
		return Rate{}, false
	}
	rows := make([][4]float64, len(observed))
	costs := make([]float64, len(observed))
	totalCost := 0.0
	for i, o := range observed {
		rows[i], costs[i] = o.Tokens, o.Cost
		totalCost += o.Cost
	}
	solution, ok := solveNormalEquations(rows, costs)
	if !ok {
		return Rate{}, false
	}
	for _, v := range solution {
		if v < 0 {
			return Rate{}, false
		}
	}
	residual := 0.0
	for i, row := range rows {
		predicted := 0.0
		for j := range row {
			predicted += row[j] * solution[j]
		}
		residual += math.Abs(predicted - costs[i])
	}
	if residual > 0.05*totalCost {
		return Rate{}, false
	}
	var mass [4]float64
	sum := 0.0
	for _, row := range rows {
		for j, v := range row {
			mass[j] += v
			sum += v
		}
	}
	if sum == 0 {
		sum = 1
	}
	family, haveFamily := builtin.Lookup(model)
	var rate Rate
	for j, field := range []string{"input", "output", "cache_write_5m", "cache_read"} {
		switch {
		case mass[j]/sum >= 0.005:
			rate.set(field, solution[j])
		case haveFamily:
			rate.set(field, family.field(field))
		default:
			return Rate{}, false
		}
	}
	rate.CacheWrite1h = rate.CacheWrite5m * 1.6
	rate.Source = "calibrated"
	return rate, true
}

// solveNormalEquations is least squares for four rates via AᵀA x = Aᵀb,
// Gauss-Jordan with partial pivoting. It reports false when the system is
// singular.
func solveNormalEquations(rows [][4]float64, costs []float64) ([4]float64, bool) {
	const n = 4
	var m [n][n + 1]float64
	for i := range n {
		for j := range n {
			for _, row := range rows {
				m[i][j] += row[i] * row[j]
			}
		}
		for k, row := range rows {
			m[i][n] += row[i] * costs[k]
		}
	}
	for c := range n {
		pivot := c
		for r := c; r < n; r++ {
			if math.Abs(m[r][c]) > math.Abs(m[pivot][c]) {
				pivot = r
			}
		}
		if math.Abs(m[pivot][c]) < 1e-12 {
			return [4]float64{}, false
		}
		m[c], m[pivot] = m[pivot], m[c]
		for r := range n {
			if r == c {
				continue
			}
			f := m[r][c] / m[c][c]
			for k := range m[r] {
				m[r][k] -= f * m[c][k]
			}
		}
	}
	var solution [4]float64
	for i := range n {
		solution[i] = m[i][n] / m[i][i]
	}
	return solution, true
}
