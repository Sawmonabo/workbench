package costs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"syscall"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const (
	ratesMaxAge  = 7 * 24 * time.Hour // refetch the official card after this
	ratesRetry   = 24 * time.Hour     // but never try more often than this
	fetchTimeout = 5 * time.Second
)

// sourceOrder ranks where a rate came from, for rates that are equally
// specific; see Lookup.
var sourceOrder = []string{"official", "calibrated", "builtin"}

// rateFields are the keys of a rate in an official price list (see
// officialFile), in the order of the ledger's token columns.
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
func matchesRate(model, prefix string, tiers bool) bool {
	modelBase, modelTier := model, ""
	prefixBase, prefixTier := prefix, ""
	if tiers {
		modelBase, modelTier = splitTier(model)
		prefixBase, prefixTier = splitTier(prefix)
	}
	if modelTier != prefixTier || !strings.HasPrefix(modelBase, prefixBase) {
		return false
	}
	rest := modelBase[len(prefixBase):]
	return rest == "" ||
		(len(rest) >= 2 && (rest[0] == '-' || rest[0] == '@') && rest[1] >= '0' && rest[1] <= '9')
}

// Lookup returns the rate that prices model, a model id of a tool whose ids
// carry a service tier when tiers is set. The row that names the model most
// specifically wins, wherever it comes from, so a page that drops
// "claude-opus-4-5" and keeps the family row "claude-opus-4" does not reprice
// the built-in "claude-opus-4-5" row away. Rows equally specific fall to the
// source order.
func (c RateCard) Lookup(model string, tiers bool) (Rate, bool) {
	var best Rate
	var bestRank [3]int
	found := false
	for prefix, rate := range c {
		if !matchesRate(model, prefix, tiers) {
			continue
		}
		if rank := rateRank(prefix, rate); !found || slices.Compare(rank[:], bestRank[:]) < 0 {
			best, bestRank, found = rate, rank, true
		}
	}
	return best, found
}

// rateRank orders the rates that match a model, best first: the most specific
// prefix, then the earlier source, then the longer prefix (so the order never
// depends on map iteration).
func rateRank(prefix string, rate Rate) [3]int {
	return [3]int{-specificity(prefix), slices.Index(sourceOrder, rate.Source), -len(prefix)}
}

// specificity is how much of a model id a prefix names: its length without a
// dated snapshot suffix ("-20251101" or "@20251101") and without a tier. Claude
// Code's own cost records, which calibration reads, are keyed by the dated id
// ("claude-opus-4-5-20251101"); that names the same model as the page's
// "claude-opus-4-5", so the two tie and the source order, official first,
// decides, as it always has.
func specificity(prefix string) int {
	base, _ := splitTier(prefix)
	n := len(base)
	if n > 9 && (base[n-9] == '-' || base[n-9] == '@') && isDigits(base[n-8:]) {
		n -= 9
	}
	return n
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// Cost is the list-price cost of the tokens at this rate in picodollars
// (1e-12 USD). Each rate, in USD per million tokens, counts to the nearest
// millionth of a dollar, which is a whole number of picodollars a token, so
// the cost is exact and any sum of costs is the same in any order or
// grouping. An int64 holds up to about 9.2 million dollars.
func (r Rate) Cost(t Tokens) int64 {
	per := func(tokens int64, rate float64) int64 { return tokens * int64(math.Round(rate*1e6)) }
	return per(t.Input, r.Input) + per(t.Output, r.Output) +
		per(t.CacheWrite5m, r.CacheWrite5m) + per(t.CacheWrite1h, r.CacheWrite1h) +
		per(t.CacheRead, r.CacheRead)
}

// Card is the merged rate card of every recorded tool.
type Card struct {
	Rows       RateCard
	Official   map[string]RateCard  // per tool: the official rows the ledger keeps
	Calibrated RateCard             // per model, from the tools' own cost records
	Fetched    map[string]time.Time // per tool: when the official list was downloaded
	Ledger     bool                 // whether there was a ledger to read the official rows from
	Notes      []string             // outcomes of the refresh this call made
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
		for name, official := range c.Official {
			tool, _ := Lookup(name)
			if o, ok := longestPrefix(model, official, tool.Tiers); ok &&
				(math.Abs(calibrated.Input-o.Input) > 0.05*math.Max(o.Input, 1e-9) ||
					math.Abs(calibrated.Output-o.Output) > 0.05*math.Max(o.Output, 1e-9)) {
				out[model] = true
			}
		}
	}
	return out
}

func longestPrefix(model string, card RateCard, tiers bool) (Rate, bool) {
	best, found := "", false
	for prefix := range card {
		if matchesRate(model, prefix, tiers) && (!found || len(prefix) > len(best)) {
			best, found = prefix, true
		}
	}
	return card[best], found
}

// Rates returns the merged card: built-in, then calibration, then the official
// price lists the ledger keeps. It opens the ledger as a report does (see
// [Paths.Open]) but never makes one: without a ledger the card holds the
// built-in and calibrated rows only, and refresh downloads nothing. With one,
// refresh downloads every official list first and reports each outcome in
// Notes.
func Rates(ctx context.Context, refresh bool) (Card, error) {
	paths, err := Locations()
	if err != nil {
		return Card{}, err
	}
	ledger, err := paths.OpenExisting(ctx)
	if err != nil {
		return Card{}, err
	}
	var notes []string
	switch {
	case ledger != nil:
		defer func() { _ = ledger.Close() }()
	case refresh:
		notes = append(notes, "rates: there is no ledger yet, so nothing was downloaded")
	}
	for _, tool := range Tools {
		if refresh && ledger != nil && tool.Source != nil && tool.Source.PricingURL() != "" {
			notes = append(notes, refreshOfficial(ctx, ledger, tool, true))
		}
	}
	card, err := loadCard(ctx, paths.Home, ledger)
	card.Notes = notes
	return card, err
}

// loadCard builds the merged card from the built-in rows, the rows calibrated
// from the tools' own cost records in home, and the official rows ledger
// keeps, none when ledger is nil.
func loadCard(ctx context.Context, home string, ledger *Ledger) (Card, error) {
	card := Card{
		Rows:     RateCard{},
		Official: map[string]RateCard{},
		Fetched:  map[string]time.Time{},
		Ledger:   ledger != nil,
	}
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		maps.Copy(card.Rows, tool.Source.Builtin())
	}
	card.Calibrated = calibrated(home)
	maps.Copy(card.Rows, card.Calibrated)
	if ledger == nil {
		return card, nil
	}
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		official, err := ledger.officialRates(ctx, tool.Name)
		if err != nil {
			return card, err
		}
		card.Official[tool.Name] = official.rates()
		if official.FetchedAt > 0 {
			card.Fetched[tool.Name] = time.Unix(int64(official.FetchedAt), 0)
		}
		maps.Copy(card.Rows, card.Official[tool.Name])
	}
	return card, nil
}

// ---------------------------------------------------------------- official card

// officialFile is one tool's official price list: its rows by model prefix
// and rate field, the page it came from, when it was downloaded and when the
// page was last asked (seconds since the epoch), and the page's validators.
// The ledger keeps it in prices and price_lists. Its JSON keys are those of
// the list file beside a ledger of schema version 5, which the upgrade reads
// (see fillPrices).
type officialFile struct {
	FetchedAt    float64                       `json:"fetched_at"`
	CheckedAt    float64                       `json:"checked_at"`
	ETag         string                        `json:"etag"`
	LastModified string                        `json:"last_modified"`
	URL          string                        `json:"url"`
	Rates        map[string]map[string]float64 `json:"rates"`
}

// rates are the list's rows that carry all five fields.
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

// fields is the rate as a price list holds it, by rate field.
func (r Rate) fields() map[string]float64 {
	out := make(map[string]float64, len(rateFields))
	for _, name := range rateFields {
		out[name] = r.field(name)
	}
	return out
}

// officialPath is one tool's price list file beside the ledger at ledger,
// <tool>/rates-official.json in its folder, where a ledger of schema version 5
// had its downloaded prices: its upgrade reads the file (see fillPrices), and
// migration 0008 deletes it (see [PriceLists]).
func officialPath(ledger string, tool Tool) string {
	return filepath.Join(filepath.Dir(ledger), tool.Name, "rates-official.json")
}

// PriceLists are the price list files beside the ledger at ledger, one per
// recorded tool, whether they are there or not (see officialPath).
func PriceLists(ledger string) []string {
	var lists []string
	for _, tool := range Tools {
		if tool.Source != nil {
			lists = append(lists, officialPath(ledger, tool))
		}
	}
	return lists
}

// fillPrices reads each tool's price list file beside the ledger at ledger
// into prices and price_lists, in tx, the transaction of the ledger's upgrade.
// A file that is missing, is not a plain file or does not parse fills nothing
// for its tool, whose list the import then downloads. A list without one
// complete row still fills its price_lists row, which keeps when the page was
// last asked, so the import still asks at most once a day.
//
// The ledger's folder moving under agent/ while it reads (see
// [MoveAgentFolders]) leaves a file where the lists were, which would fill
// nothing for a list that is there: it then fails, which undoes the upgrade,
// and the open is tried again where the ledger is now (see [Paths.moved]).
func fillPrices(ctx context.Context, tx *sql.Tx, ledger string) error {
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		path := officialPath(ledger, tool)
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var file officialFile
		if json.Unmarshal(data, &file) != nil {
			continue
		}
		if err := storeOfficial(ctx, tx, tool.Name, file); err != nil {
			return err
		}
	}
	if folder := filepath.Dir(ledger); !operation.IsFolder(folder) {
		return operation.FileError(folder,
			fmt.Errorf("moved while its price lists were read: %w", syscall.ENOTDIR))
	}
	return nil
}

// officialRates is tool's official price list as the ledger keeps it: the
// zero list, with no rows, when it keeps none.
func (l *Ledger) officialRates(ctx context.Context, tool string) (officialFile, error) {
	file := officialFile{Rates: map[string]map[string]float64{}}
	err := l.db.QueryRowContext(
		ctx,
		"SELECT url, fetched_at, checked_at, etag, last_modified FROM price_lists WHERE tool = ?",
		tool,
	).Scan(&file.URL, &file.FetchedAt, &file.CheckedAt, &file.ETag, &file.LastModified)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return file, err
	}
	rows, err := l.db.QueryContext(
		ctx,
		`SELECT prefix, input, output, cache_write_5m, cache_write_1h, cache_read
		   FROM prices WHERE tool = ?`,
		tool,
	)
	if err != nil {
		return file, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var prefix string
		var rate Rate
		if err := rows.Scan(
			&prefix,
			&rate.Input,
			&rate.Output,
			&rate.CacheWrite5m,
			&rate.CacheWrite1h,
			&rate.CacheRead,
		); err != nil {
			return file, err
		}
		file.Rates[prefix] = rate.fields()
	}
	return file, rows.Err()
}

// replaceOfficial makes file tool's official price list, in one short
// transaction: its complete rows replace the tool's prices, and its times and
// validators the tool's price_lists row.
func (l *Ledger) replaceOfficial(ctx context.Context, tool string, file officialFile) error {
	return l.transact(ctx, func(tx *sql.Tx) error { return storeOfficial(ctx, tx, tool, file) })
}

// storeOfficial is [Ledger.replaceOfficial] in tx.
func storeOfficial(ctx context.Context, tx *sql.Tx, tool string, file officialFile) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM prices WHERE tool = ?", tool); err != nil {
		return err
	}
	for prefix, rate := range file.rates() {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO prices (tool, prefix, input, output, cache_write_5m, cache_write_1h, cache_read)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			tool,
			prefix,
			rate.Input,
			rate.Output,
			rate.CacheWrite5m,
			rate.CacheWrite1h,
			rate.CacheRead,
		); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO price_lists (tool, url, fetched_at, checked_at, etag, last_modified)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tool) DO UPDATE SET url = excluded.url, fetched_at = excluded.fetched_at,
		   checked_at = excluded.checked_at, etag = excluded.etag,
		   last_modified = excluded.last_modified`,
		tool, file.URL, file.FetchedAt, file.CheckedAt, file.ETag, file.LastModified,
	)
	return err
}

// recordAttempt records in tool's price_lists row that its page was asked at
// checked and, when fetched is not zero, found unchanged then. The tool's
// prices, and the rest of the row, stay as they are, whatever their age.
func (l *Ledger) recordAttempt(ctx context.Context, tool string, checked, fetched float64) error {
	_, err := l.db.ExecContext(
		ctx,
		`INSERT INTO price_lists (tool, url, fetched_at, checked_at, etag, last_modified)
		 VALUES (?, '', ?, ?, '', '')
		 ON CONFLICT(tool) DO UPDATE SET checked_at = excluded.checked_at,
		   fetched_at = max(fetched_at, excluded.fetched_at)`,
		tool, fetched, checked,
	)
	return err
}

func epoch(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

// refreshOfficial is a conditional GET of the tool's pricing page, made with
// no transaction open, and then one short transaction that stores the list it
// brought (see [Ledger.replaceOfficial]); it needs no import lock. It is best
// effort: any failure, or a page that holds no price, keeps the tool's prices,
// records the attempt time and returns a one-line reason. Without force a
// fresh enough list is left alone.
func refreshOfficial(ctx context.Context, ledger *Ledger, tool Tool, force bool) string {
	label := "rates"
	if tool.Name != firstTool {
		label += " " + tool.Name
	}
	cached, err := ledger.officialRates(ctx, tool.Name)
	if err != nil {
		return fmt.Sprintf("%s: the official card can't be read (%v)", label, err)
	}
	age := time.Since(time.Unix(0, int64(cached.FetchedAt*1e9)))
	if !force && len(cached.rates()) > 0 && age < ratesMaxAge {
		return fmt.Sprintf("%s: cached (%.1fd old)", label, age.Hours()/24)
	}
	checked, fetched := epoch(time.Now()), 0.0
	keep := func(msg string) string {
		_ = ledger.recordAttempt(ctx, tool.Name, checked, fetched)
		return msg
	}
	url := tool.Source.PricingURL()
	// The timeout bounds the download only; the ledger is written with ctx.
	download, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(download, http.MethodGet, url, nil)
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
		fetched = epoch(time.Now())
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
		file.Rates[model] = rate.fields()
	}
	if err := ledger.replaceOfficial(ctx, tool.Name, file); err != nil {
		return fmt.Sprintf("%s: fetched but could not save the card (%v)", label, err)
	}
	return fmt.Sprintf("%s: official card updated (%d models)", label, len(parsed))
}

// refreshIfNeeded is the worker's refresh policy: fetch when a tool's official
// card is missing or older than seven days, or when a ledger model has no rate
// anywhere; in all cases at most once a day. home holds the tools' own cost
// records (see calibrated).
func refreshIfNeeded(ctx context.Context, ledger *Ledger, home string) string {
	card, err := loadCard(ctx, home, ledger)
	if err != nil {
		return fmt.Sprintf("rates: the prices can't be read (%v)", err)
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
		unpriced, err := unpricedModels(ctx, ledger, card, tool)
		if err != nil {
			messages = append(
				messages,
				fmt.Sprintf("%s: the ledger's models can't be read (%v)", label, err),
			)
			continue
		}
		cached, err := ledger.officialRates(ctx, tool.Name)
		if err != nil {
			messages = append(
				messages,
				fmt.Sprintf("%s: the official card can't be read (%v)", label, err),
			)
			continue
		}
		age := time.Since(time.Unix(0, int64(cached.FetchedAt*1e9)))
		sinceCheck := time.Since(time.Unix(0, int64(cached.CheckedAt*1e9)))
		var msg string
		switch {
		case len(cached.rates()) > 0 && age < ratesMaxAge && len(unpriced) == 0:
			msg = fmt.Sprintf("%s: cached (%.1fd old)", label, age.Hours()/24)
		case sinceCheck >= ratesRetry:
			msg = refreshOfficial(ctx, ledger, tool, true)
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
			msg += "; unpriced: " + strings.Join(names, ", ")
		}
		messages = append(messages, msg)
	}
	return strings.Join(messages, "; ")
}

// Unpriced is a model no rate prices, and why. Its tokens count; its cost
// counts as 0 until an official price list prices it.
type Unpriced struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// unpriced is why no rate prices model, one of tool's: the transcript named
// no model, the price page has the model but not at its tier, or the page
// does not have it at all.
func (c Card) unpriced(tool Tool, model string) Unpriced {
	base, tier := tool.SplitModel(model)
	reason := "not on " + tool.PricePage
	switch _, standard := c.Rows.Lookup(base, tool.Tiers); {
	case base == "unknown":
		reason = "the transcript names no model"
	case tier != "" && standard:
		reason = "no " + tier + " price on " + tool.PricePage
	}
	return Unpriced{Model: model, Reason: reason}
}

// unpricedModels are the tool's ledger models no source prices. A read of the
// ledger that fails is its error, never a shorter list.
func unpricedModels(ctx context.Context, ledger *Ledger, card Card, tool Tool) ([]Unpriced, error) {
	rows, err := ledger.db.QueryContext(
		ctx,
		"SELECT DISTINCT model FROM responses WHERE tool = ?",
		tool.Name,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Unpriced
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, err
		}
		if _, ok := card.Rows.Lookup(model, tool.Tiers); !ok {
			out = append(out, card.unpriced(tool, model))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out, nil
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
			if rate, ok := calibrate(model, observed, builtin, tool.Tiers); ok {
				out[model] = rate
			}
		}
	}
	return out
}

func calibrate(model string, observed []Observation, builtin RateCard, tiers bool) (Rate, bool) {
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
	family, haveFamily := builtin.Lookup(model, tiers)
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
