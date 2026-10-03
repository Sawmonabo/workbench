package costs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// claude is the Claude Code source: JSONL transcripts under
// <config dir>/projects, where the config dir is CLAUDE_CONFIG_DIR, else
// ~/.claude.
type claude struct{}

func (claude) Name() string { return "claude" }

// claudeDir is where Claude Code keeps its state.
func claudeDir(home string) string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".claude")
}

// claudeJSON is Claude Code's account and usage file: inside CLAUDE_CONFIG_DIR
// when that is set, directly in the home directory otherwise.
func claudeJSON(home string) string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// claudeProjects is the directory Claude Code writes its transcripts under.
func claudeProjects(home string) string { return filepath.Join(claudeDir(home), "projects") }

// TranscriptRoots is where this tool's transcripts live.
func (claude) TranscriptRoots(home string) []string { return []string{claudeProjects(home)} }

// Transcripts lists every *.jsonl under projects/, subagent transcripts
// included.
func (claude) Transcripts(home string) ([]string, error) {
	root := claudeProjects(home)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, nil
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			// A directory that vanished or cannot be read holds nothing to ingest.
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		case filepath.Ext(path) != ".jsonl":
		case entry.Type().IsRegular():
			files = append(files, path)
		case entry.Type()&fs.ModeSymlink != 0:
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
				files = append(files, path)
			}
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// claudeOAuth is the oauthAccount a .claude.json keeps: who is signed in and
// the organization that pays for it.
type claudeOAuth struct {
	EmailAddress     string `json:"emailAddress"`
	OrganizationUUID string `json:"organizationUuid"`
	OrganizationName string `json:"organizationName"`
	OrganizationType string `json:"organizationType"`
}

// readClaudeOAuth is the oauthAccount of one .claude.json; false when the
// file cannot be read or holds none.
func readClaudeOAuth(path string) (claudeOAuth, bool) {
	var data struct {
		OAuthAccount *claudeOAuth `json:"oauthAccount"`
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &data) != nil || data.OAuthAccount == nil {
		return claudeOAuth{}, false
	}
	return *data.OAuthAccount, true
}

// SignIn is the sign-in Claude Code keeps in ~/.claude.json oauthAccount: its
// email, the subscription "claude:<organizationUuid>" and the label the report
// shows. Without an oauthAccount (an API key, or not signed in) the account is
// "unknown" and the subscription is empty; an oauthAccount without an
// organizationUuid also leaves the subscription empty, since "claude:" would
// merge unrelated sign-ins.
func (claude) SignIn(home string) SignIn {
	acct, ok := readClaudeOAuth(claudeJSON(home))
	if !ok {
		return SignIn{Account: "unknown"}
	}
	out := SignIn{Account: firstNonEmpty(acct.EmailAddress, "unknown")}
	if acct.OrganizationUUID == "" {
		return out
	}
	out.Subscription = "claude:" + acct.OrganizationUUID
	out.Label = claudePlanLabel(acct.OrganizationType, acct.OrganizationName)
	return out
}

// claudeSignIns are the oauthAccounts Claude Code keeps on disk, oldest
// first: the copies of .claude.json it writes to backups/ before changing it
// (named .claude.json.backup.<milliseconds>), then .claude.json itself. A
// backup keeps the organization of an earlier sign-in, which transcripts
// still name.
func claudeSignIns(home string) []claudeOAuth {
	backups, _ := filepath.Glob(filepath.Join(claudeDir(home), "backups", ".claude.json.backup.*"))
	stamp := func(path string) int64 {
		n, _ := strconv.ParseInt(strings.TrimPrefix(filepath.Ext(path), "."), 10, 64)
		return n
	}
	sort.Slice(backups, func(i, j int) bool { return stamp(backups[i]) < stamp(backups[j]) })
	var out []claudeOAuth
	for _, path := range append(backups, claudeJSON(home)) {
		if acct, ok := readClaudeOAuth(path); ok && acct.OrganizationUUID != "" {
			out = append(out, acct)
		}
	}
	return out
}

// Accounts maps each organization a Claude Code sign-in on disk belongs to
// (claudeSignIns) to the email signed in to it. A transcript's credential_org
// names the organization that answered (Usage.AccountKey). An organization
// two emails were signed in to is left out rather than guessed, so its rows
// fall back to the session and observed evidence.
func (claude) Accounts(home string) map[string]string {
	accounts := map[string]string{}
	ambiguous := map[string]bool{}
	for _, acct := range claudeSignIns(home) {
		if acct.EmailAddress == "" {
			continue
		}
		if prev, seen := accounts[acct.OrganizationUUID]; seen && prev != acct.EmailAddress {
			ambiguous[acct.OrganizationUUID] = true
		}
		accounts[acct.OrganizationUUID] = acct.EmailAddress
	}
	for id := range ambiguous {
		delete(accounts, id)
	}
	return accounts
}

// Labels maps the subscription id of each organization a sign-in on disk
// belongs to (claudeSignIns) to its report label, the latest sign-in's.
func (claude) Labels(home string) map[string]string {
	labels := map[string]string{}
	for _, acct := range claudeSignIns(home) {
		if label := claudePlanLabel(acct.OrganizationType, acct.OrganizationName); label != "" {
			labels["claude:"+acct.OrganizationUUID] = label
		}
	}
	return labels
}

// claudePlanLabel is the report label of an organizationType: the type without
// its "claude_" prefix and with the first letter capitalised, so claude_max is
// "Max" and claude_team is "Team". A personal plan (claude_max, claude_pro) is
// named by that alone; any other type adds the organization, "Team (Example
// Org)", because many organizations share one type.
func claudePlanLabel(orgType, orgName string) string {
	label := strings.TrimPrefix(orgType, "claude_")
	if label != "" {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	if orgType == "claude_max" || orgType == "claude_pro" || orgName == "" {
		return label
	}
	if label == "" {
		return orgName
	}
	return label + " (" + orgName + ")"
}

// projectFromPath is the last-resort project for records without a cwd: the
// project directory name decoded. Claude Code encodes '/' as '-', so names
// containing '-' are ambiguous; records normally carry their own cwd.
func projectFromPath(path string) string {
	home, _ := os.UserHomeDir()
	name := filepath.Base(filepath.Dir(path))
	if rel, err := filepath.Rel(filepath.Join(claudeDir(home), "projects"), path); err == nil &&
		!strings.HasPrefix(rel, "..") {
		name = strings.Split(rel, string(filepath.Separator))[0]
	}
	return "/" + strings.ReplaceAll(strings.TrimLeft(name, "-"), "-", "/")
}

// tokens are the counts of a usage object or one of its iterations.
type tokens struct {
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheCreation *struct {
		Ephemeral5m *int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	CacheCreationInput int64 `json:"cache_creation_input_tokens"`
	CacheRead          int64 `json:"cache_read_input_tokens"`
}

// fill copies the counts into u. Without the per-lifetime split the whole
// cache-creation count is a five-minute write.
func (t tokens) fill(u *Usage) {
	u.Input, u.Output, u.CacheRead = t.Input, t.Output, t.CacheRead
	cc := t.CacheCreation
	if cc == nil || (cc.Ephemeral5m == nil && cc.Ephemeral1h == nil) {
		u.CacheWrite5m, u.CacheWrite1h = t.CacheCreationInput, 0
		return
	}
	if cc.Ephemeral5m != nil {
		u.CacheWrite5m = *cc.Ephemeral5m
	}
	if cc.Ephemeral1h != nil {
		u.CacheWrite1h = *cc.Ephemeral1h
	}
}

type record struct {
	Type       string          `json:"type"`
	Attachment json.RawMessage `json:"attachment"`
	Cwd        string          `json:"cwd"`
	SessionID  string          `json:"sessionId"`
	RequestID  string          `json:"requestId"`
	UUID       string          `json:"uuid"`
	Timestamp  string          `json:"timestamp"`
	Message    json.RawMessage `json:"message"`
}

type message struct {
	ID    string          `json:"id"`
	Model string          `json:"model"`
	Usage json.RawMessage `json:"usage"`
}

type usage struct {
	tokens
	Iterations []json.RawMessage `json:"iterations"`
}

type iteration struct {
	tokens
	Type  string `json:"type"`
	Model string `json:"model"`
}

// claudeState is what the Claude parser keeps between the lines of a
// transcript and, saved, between ingest runs.
type claudeState struct {
	// Org is the organization of the latest credential_org attachment read in
	// this file: the organization Claude Code's credential belongs to from
	// that line on. Claude Code writes one when a session starts or resumes,
	// at intervals, and after a /login switches organization.
	Org string `json:"org,omitempty"`
}

func claudeStateOf(file *FileState) *claudeState {
	if state, ok := file.cache.(*claudeState); ok {
		return state
	}
	state := &claudeState{}
	if len(file.Saved) > 0 {
		_ = json.Unmarshal(file.Saved, state) // damaged state reads as empty
	}
	file.cache = state
	return state
}

// ParseRevision changes when Parse learns to read something from lines it
// has already read: ingest then reads every Claude Code transcript again from
// the start, once. "2" reads credential_org attachments.
func (claude) ParseRevision() string { return "2" }

// decode fills v from raw, tolerating fields of the wrong type (they stay
// zero) but not a syntax error.
func decode(raw []byte, v any) bool {
	err := json.Unmarshal(raw, v)
	var wrong *json.UnmarshalTypeError
	return err == nil || errors.As(err, &wrong)
}

// Parse returns the usage rows of one transcript line: the response itself,
// plus one row per advisor call. An advisor runs on another model and its
// usage appears only in usage.iterations (type "advisor_message"), never in
// the top-level counts; it is keyed <request id>:<iteration index>, which
// stays stable as later records of the same request add iterations. A row
// after a credential_org attachment is that organization's: its
// subscription, and the email signed in to it (AccountKey).
func (claude) Parse(line []byte, file *FileState) []Usage {
	if !bytes.Contains(line, []byte(`"usage"`)) && !bytes.Contains(line, []byte(`"cwd"`)) &&
		!bytes.Contains(line, []byte(`"credential_org"`)) {
		return nil
	}
	var rec record
	if !decode(line, &rec) {
		return nil // a malformed line is skipped
	}
	if rec.Cwd != "" {
		file.LastCwd = rec.Cwd
	}
	if rec.Type == "attachment" {
		var attachment struct {
			Type string `json:"type"`
			Org  string `json:"organizationUuid"`
		}
		if decode(rec.Attachment, &attachment) && attachment.Type == "credential_org" &&
			attachment.Org != "" {
			if state := claudeStateOf(file); state.Org != attachment.Org {
				state.Org = attachment.Org
				file.Saved, _ = json.Marshal(state)
			}
		}
		return nil
	}
	if rec.Type != "assistant" {
		return nil
	}
	var msg message
	if !decode(rec.Message, &msg) || len(msg.Usage) == 0 || msg.Usage[0] != '{' {
		return nil
	}
	model := msg.Model
	if model == "" {
		model = "unknown"
	}
	id := firstNonEmpty(rec.RequestID, msg.ID, rec.UUID)
	if model == "<synthetic>" || id == "" {
		return nil
	}
	var u usage
	if !decode(msg.Usage, &u) {
		return nil
	}
	if file.Fallback == "" {
		file.Fallback = projectFromPath(file.Path)
	}
	when, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
	base := Usage{
		Tool:      "claude",
		Time:      when,
		Project:   firstNonEmpty(rec.Cwd, file.LastCwd, file.Fallback),
		Session:   rec.SessionID,
		Root:      rec.SessionID,
		RequestID: id,
		Model:     model,
	}
	if org := claudeStateOf(file).Org; org != "" {
		base.Subscription, base.SubscriptionEvidence = "claude:"+org, EvidenceTranscript
		base.AccountKey = org
	}
	response := base
	u.fill(&response)
	rows := []Usage{response}
	for i, raw := range u.Iterations {
		var it iteration
		if !decode(raw, &it) || it.Type != "advisor_message" {
			continue
		}
		row := base
		row.RequestID = id + ":" + strconv.Itoa(i)
		row.Model = firstNonEmpty(it.Model, model)
		it.fill(&row)
		rows = append(rows, row)
	}
	return rows
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Observations are the (tokens, costUSD) pairs Claude Code keeps per project
// and model in .claude.json (lastModelUsage), for calibration.
func (claude) Observations(home string) []Observation {
	var data struct {
		Projects map[string]struct {
			LastModelUsage map[string]struct {
				Input         float64 `json:"inputTokens"`
				Output        float64 `json:"outputTokens"`
				CacheCreation float64 `json:"cacheCreationInputTokens"`
				CacheRead     float64 `json:"cacheReadInputTokens"`
				Cost          float64 `json:"costUSD"`
			} `json:"lastModelUsage"`
		} `json:"projects"`
	}
	raw, err := os.ReadFile(claudeJSON(home))
	if err != nil || !decode(raw, &data) {
		return nil
	}
	var out []Observation
	for _, project := range data.Projects {
		for model, u := range project.LastModelUsage {
			x := [4]float64{u.Input / 1e6, u.Output / 1e6, u.CacheCreation / 1e6, u.CacheRead / 1e6}
			if u.Cost > 0 && x != [4]float64{} {
				out = append(out, Observation{Model: model, Tokens: x, Cost: u.Cost})
			}
		}
	}
	return out
}

// Hooks reports, for each session event, whether Claude Code's settings.json
// runs the ingest hook synchronously: HookOK, else HookMissing. An async hook
// does not count: Claude Code kills a background hook still running when a
// session ends in headless (-p) mode, and the ingest hook returns within
// milliseconds anyway. Claude Code has no hook trust or per-hook switch.
func (claude) Hooks(home string) map[string]string {
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if raw, err := os.ReadFile(filepath.Join(claudeDir(home), "settings.json")); err == nil {
		decode(raw, &settings)
	}
	out := map[string]string{}
	for _, event := range []string{"SessionStart", "SessionEnd"} {
		installed := false
		for _, group := range settings.Hooks[event] {
			for _, hook := range group.Hooks {
				// "claude-costs ingest", the replaced script, also contains
				// "costs ingest", so the Workbench command is matched whole.
				installed = installed ||
					(strings.Contains(hook.Command, "workbench costs ingest") && !hook.Async)
			}
		}
		out[event] = HookMissing
		if installed {
			out[event] = HookOK
		}
	}
	return out
}

// PricingURL is the official price page; CLAUDE_COSTS_PRICING_URL replaces it
// for checks that must not reach the network.
func (claude) PricingURL() string {
	if url := os.Getenv("CLAUDE_COSTS_PRICING_URL"); url != "" {
		return url
	}
	return "https://platform.claude.com/docs/en/about-claude/pricing.md"
}

// builtin is the list prices read from the pricing page on 2026-09-30:
// input, output, 5m write, 1h write, cache read per million tokens. Longest
// prefix wins, so a family row is the fallback for ids no specific row
// covers. The official card and calibration override these at runtime; the
// manual overrides file wins over everything.
var builtin = map[string][5]float64{
	"claude-fable-5-1":  {10, 50, 12.5, 20, 0.25},
	"claude-fable-5":    {10, 50, 12.5, 20, 1},
	"claude-fable":      {10, 50, 12.5, 20, 1},
	"claude-mythos-5-1": {10, 50, 12.5, 20, 0.25},
	"claude-mythos-5":   {10, 50, 12.5, 20, 1},
	"claude-opus-5-5":   {4, 20, 5, 8, 0.2},
	"claude-opus-5":     {5, 25, 6.25, 10, 0.5},
	"claude-opus-4-8":   {5, 25, 6.25, 10, 0.5},
	"claude-opus-4-7":   {5, 25, 6.25, 10, 0.5},
	"claude-opus-4-6":   {5, 25, 6.25, 10, 0.5},
	"claude-opus-4-5":   {5, 25, 6.25, 10, 0.5},
	"claude-opus-4-1":   {15, 75, 18.75, 30, 1.5},
	"claude-opus-4":     {15, 75, 18.75, 30, 1.5},
	"claude-opus":       {5, 25, 6.25, 10, 0.5},
	"claude-sonnet-5-5": {2, 10, 2.5, 4, 0.2},
	"claude-sonnet-5":   {2, 10, 2.5, 4, 0.2},
	"claude-sonnet-4-6": {3, 15, 3.75, 6, 0.3},
	"claude-sonnet-4-5": {3, 15, 3.75, 6, 0.3},
	"claude-sonnet-4":   {3, 15, 3.75, 6, 0.3},
	"claude-sonnet":     {3, 15, 3.75, 6, 0.3},
	"claude-haiku-4-5":  {1, 5, 1.25, 2, 0.1},
	"claude-haiku-3-5":  {0.8, 4, 1, 1.6, 0.08},
	"claude-haiku":      {1, 5, 1.25, 2, 0.1},
}

func (claude) Builtin() RateCard {
	card := RateCard{}
	for prefix, v := range builtin {
		card[prefix] = Rate{v[0], v[1], v[2], v[3], v[4], "builtin"}
	}
	return card
}

var (
	moneyPattern  = regexp.MustCompile(`\$\s*([0-9]+(?:\.[0-9]+)?)`)
	markupPattern = regexp.MustCompile(
		"[`*_]|<[^>]*>|\\[|\\]\\([^)]*\\)",
	) // emphasis, HTML tags, link targets
	notePattern = regexp.MustCompile(
		`\s*\(.*$`,
	) // a trailing note such as "(retired)"
	spacePattern = regexp.MustCompile(`[.\s]+`)
)

// normalizeModel turns 'Claude Opus 5.5' into 'claude-opus-5-5'; an id is
// returned as is. A cell that names several models ('/', ',', ' and ') or none
// gives "".
func normalizeModel(name string) string {
	s := strings.ToLower(
		strings.TrimSpace(
			notePattern.ReplaceAllString(markupPattern.ReplaceAllString(name, ""), ""),
		),
	)
	if !strings.HasPrefix(s, "claude") || strings.ContainsAny(s, "/,") ||
		strings.Contains(s, " and ") {
		return ""
	}
	if !strings.Contains(s, " ") {
		if strings.HasPrefix(s, "claude-") {
			return s
		}
		return ""
	}
	s = strings.Trim(spacePattern.ReplaceAllString(strings.TrimSpace(s[len("claude"):]), "-"), "-")
	if s == "" {
		return ""
	}
	return "claude-" + s
}

func findColumn(headers []string, keys []string, exclude ...string) int {
	for j, h := range headers {
		if containsAny(h, keys) && !containsAny(h, exclude) {
			return j
		}
	}
	return -1
}

func containsAny(s string, parts []string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// money reads the dollar amount of cell j.
func money(cells []string, j int) (float64, bool) {
	if j < 0 || j >= len(cells) {
		return 0, false
	}
	m := moneyPattern.FindStringSubmatch(cells[j])
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	return v, err == nil
}

// ParsePricing reads every Markdown table with an input and an output column.
// The first table that prices a model wins, and tables under a batch or
// fast-mode heading are skipped: the page repeats model names there at
// discounted or premium rates.
func (claude) ParsePricing(page string) RateCard {
	card := RateCard{}
	lines := strings.Split(page, "\n")
	heading := ""
	for i := 0; i < len(lines)-1; {
		head, sep := strings.TrimSpace(lines[i]), strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(head, "#") {
			heading = strings.ToLower(head)
		}
		if !strings.HasPrefix(head, "|") || !strings.HasPrefix(sep, "|") ||
			strings.Trim(sep, "|-: ") != "" {
			i++
			continue
		}
		var headers []string
		for h := range strings.SplitSeq(strings.Trim(head, "|"), "|") {
			headers = append(headers, strings.ToLower(strings.TrimSpace(h)))
		}
		ci := findColumn(headers, []string{"input"}, "cache", "batch", "additional")
		co := findColumn(headers, []string{"output"}, "batch")
		c5 := findColumn(headers, []string{"5m", "5-minute"}, "1h")
		if c5 < 0 {
			c5 = findColumn(headers, []string{"write"}, "1h")
		}
		c1 := findColumn(headers, []string{"1h", "1-hour"})
		cr := findColumn(headers, []string{"hit", "read", "refresh"})
		i += 2
		if ci < 0 || co < 0 || strings.Contains(heading, "batch") ||
			strings.Contains(heading, "fast") {
			continue
		}
		for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
			var cells []string
			for c := range strings.SplitSeq(strings.Trim(strings.TrimSpace(lines[i]), "|"), "|") {
				cells = append(cells, strings.TrimSpace(c))
			}
			model := normalizeModel(cells[0])
			if _, seen := card[model]; model == "" || seen {
				continue
			}
			in, okIn := money(cells, ci)
			out, okOut := money(cells, co)
			if !okIn || !okOut {
				continue
			}
			rate := Rate{
				Input:        in,
				Output:       out,
				CacheWrite5m: in * 1.25,
				CacheWrite1h: in * 2,
				CacheRead:    in * 0.1,
			}
			if v, ok := money(cells, c5); ok {
				rate.CacheWrite5m = v
			}
			if v, ok := money(cells, c1); ok {
				rate.CacheWrite1h = v
			}
			if v, ok := money(cells, cr); ok {
				rate.CacheRead = v
			}
			card[model] = rate
		}
	}
	return card
}
