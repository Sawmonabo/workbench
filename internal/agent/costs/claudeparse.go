package costs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// projectFromPath is the last-resort project for records without a cwd: the
// project directory name decoded. Claude Code encodes '/' as '-', so names
// containing '-' are ambiguous; records normally carry their own cwd.
func projectFromPath(path string) string {
	home, _ := os.UserHomeDir()
	name := filepath.Base(filepath.Dir(path))
	if projects := filepath.Join(ClaudeDir(home), "projects"); operation.Within(projects, path) {
		rel, _ := filepath.Rel(projects, path)
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
	Account    string          `json:"accountUuid"`
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
	// Speed is "fast" for a response sent in fast mode, which the price page
	// bills at its own, higher rates; "standard" otherwise.
	Speed      string            `json:"speed"`
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
	// Last is the latest timestamp read in this file, the time of a record
	// that has none of its own.
	Last string `json:"last,omitempty"`

	last  []byte // Last while a read is under way, reused from line to line
	dirty bool   // the state changed since it was last saved
}

func claudeStateOf(file *FileState) *claudeState {
	if state, ok := file.cache.(*claudeState); ok {
		return state
	}
	state := &claudeState{}
	if len(file.Saved) > 0 {
		_ = json.Unmarshal(file.Saved, state) // damaged state reads as empty
	}
	state.last = append(state.last, state.Last...)
	file.cache = state
	return state
}

// Finish saves the state a read of the file ended with.
func (claude) Finish(file *FileState) {
	state, ok := file.cache.(*claudeState)
	if !ok || !state.dirty && string(state.last) == state.Last {
		return
	}
	state.Last, state.dirty = string(state.last), false
	file.Saved, _ = json.Marshal(state)
}

// ParseRevision changes when Parse learns to read something from lines it
// has already read: ingest then reads every Claude Code transcript again from
// the start, once. "2" reads credential_org attachments; "3" reads usage.speed;
// "4" reads the account marks.
func (claude) ParseRevision() string { return "4" }

// Markers of the lines that hold an account mark.
var (
	timestampKey = []byte(`"timestamp":"`)
	loginOutput  = []byte(`Login successful</local-command-stdout>`)
	accountKey   = []byte(`"accountUuid"`)
)

// The records that name who was signed in. A /login that succeeds writes
// loginStdout as its own user record, a plain string; a tool result that only
// quotes it is not one. accountLedger is a record the claude.ai artifact
// tools keep in the session, naming the account (accountUuid) of the
// credential the session holds at that point; it has no time of its own.
const (
	loginStdout   = "<local-command-stdout>Login successful</local-command-stdout>"
	accountLedger = "artifact-autoreact-ledger"
)

// noteTime keeps the line's first timestamp as the file's latest, without
// decoding the line. A timestamp quoted inside a message is escaped and never
// matches; any other is the record's own or one of its parts', which is as
// close to the time of the next line.
func noteTime(line []byte, state *claudeState) {
	_, rest, found := bytes.Cut(line, timestampKey)
	if !found {
		return
	}
	end := bytes.IndexByte(rest, '"')
	if end < len("2006-01-02T15:04:05Z") || rest[0] < '0' || rest[0] > '9' {
		return
	}
	state.last = append(state.last[:0], rest[:end]...)
}

// mark adds an account mark at time when, ignoring one with no valid time.
func mark(file *FileState, kind, when, account, org string) {
	at, err := time.Parse(time.RFC3339Nano, when)
	if err != nil {
		return
	}
	file.Marks = append(file.Marks, AccountMark{Kind: kind, Time: at, Account: account, Org: org})
}

// readMarks adds the account marks of a decoded record: a successful /login,
// an organization, or an account id at the time of the line before it.
func readMarks(line []byte, rec record, file *FileState, state *claudeState) {
	switch {
	case rec.Type == "user" && bytes.Contains(line, loginOutput):
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		var content string
		if decode(rec.Message, &msg) && json.Unmarshal(msg.Content, &content) == nil &&
			strings.TrimSpace(content) == loginStdout {
			mark(file, MarkLogin, rec.Timestamp, "", "")
		}
	case rec.Type == accountLedger && rec.Account != "":
		mark(file, MarkAccount, string(state.last), rec.Account, "")
	}
}

// Parse returns the usage rows of one transcript line: the response itself,
// plus one row per advisor call. An advisor runs on another model and its
// usage appears only in usage.iterations (type "advisor_message"), never in
// the top-level counts; it is keyed <request id>:<iteration index>, which
// stays stable as later records of the same request add iterations. A row
// after a credential_org attachment is that organization's: its
// subscription, and the email signed in to it (AccountKey). A fast-mode
// response is the model at its fast service tier, "<model>@fast", as a Codex
// one is: the price page bills it at other rates. An advisor row keeps the
// advisor's own model, billed per its iteration, which carries no speed.
func (claude) Parse(line []byte, file *FileState) []Usage {
	state := claudeStateOf(file)
	// A mark on this line takes the time of the line before it.
	defer noteTime(line, state)
	if !bytes.Contains(line, []byte(`"usage"`)) && !bytes.Contains(line, []byte(`"cwd"`)) &&
		!bytes.Contains(line, []byte(`"credential_org"`)) && !bytes.Contains(line, accountKey) {
		return nil
	}
	var rec record
	if !decode(line, &rec) {
		return nil // a malformed line is skipped
	}
	if rec.Cwd != "" {
		file.LastCwd = rec.Cwd
	}
	readMarks(line, rec, file, state)
	if rec.Type == "attachment" {
		var attachment struct {
			Type string `json:"type"`
			Org  string `json:"organizationUuid"`
		}
		if decode(rec.Attachment, &attachment) && attachment.Type == "credential_org" &&
			attachment.Org != "" {
			mark(file, MarkOrg, rec.Timestamp, "", attachment.Org)
			if state.Org != attachment.Org {
				state.Org, state.dirty = attachment.Org, true
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
	if org := state.Org; org != "" {
		base.Subscription, base.SubscriptionEvidence = "claude:"+org, EvidenceTranscript
		base.AccountKey = org
	}
	response := base
	if u.Speed == fastSpeed {
		response.Model = model + "@" + fastSpeed
	}
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

// fastSpeed is the usage.speed of a fast-mode response, and the service tier
// its model id carries.
const fastSpeed = "fast"
