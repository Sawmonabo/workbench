package costs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// claudeOAuth is the oauthAccount a .claude.json keeps: who is signed in and
// the organization that pays for it.
type claudeOAuth struct {
	AccountUUID      string `json:"accountUuid"`
	EmailAddress     string `json:"emailAddress"`
	OrganizationUUID string `json:"organizationUuid"`
	OrganizationName string `json:"organizationName"`
	OrganizationType string `json:"organizationType"`
	PlanDisplayName  string `json:"planDisplayName"`
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
	acct, ok := readClaudeOAuth(toolconfig.ClaudeJSON(home))
	if !ok {
		return SignIn{Account: "unknown"}
	}
	out := SignIn{Account: firstNonEmpty(acct.EmailAddress, "unknown")}
	if acct.OrganizationUUID == "" {
		return out
	}
	out.Subscription = "claude:" + acct.OrganizationUUID
	out.Label = claudePlanLabel(acct)
	return out
}

// claudeSignIns are the oauthAccounts Claude Code keeps on disk, oldest
// first: the copies of .claude.json it writes to backups/ before changing it
// (named .claude.json.backup.<milliseconds>), then .claude.json itself. A
// backup keeps the organization of an earlier sign-in, which transcripts
// still name.
func claudeSignIns(home string) []claudeOAuth {
	backups, _ := filepath.Glob(
		filepath.Join(toolconfig.ClaudeDir(home), "backups", ".claude.json.backup.*"),
	)
	stamp := func(path string) int64 {
		n, _ := strconv.ParseInt(strings.TrimPrefix(filepath.Ext(path), "."), 10, 64)
		return n
	}
	sort.Slice(backups, func(i, j int) bool { return stamp(backups[i]) < stamp(backups[j]) })
	var out []claudeOAuth
	for _, path := range append(backups, toolconfig.ClaudeJSON(home)) {
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

// SignIns are the sign-ins on disk (claudeSignIns), oldest first, each as
// SignIn names the current one.
func (claude) SignIns(home string) []SignIn {
	var out []SignIn
	for _, acct := range claudeSignIns(home) {
		out = append(out, SignIn{
			Account:      firstNonEmpty(acct.EmailAddress, "unknown"),
			Subscription: "claude:" + acct.OrganizationUUID,
			Label:        claudePlanLabel(acct),
		})
	}
	return out
}

// Marks are the account marks Claude Code keeps outside its transcripts: a
// /login typed at its prompt (history.jsonl), each one a sign-in that may
// have succeeded, and the account id and organization of each sign-in on disk
// (claudeSignIns), which pair the two. A /login that failed only splits a
// login stretch in two, which names fewer rows, never another account.
//
// It returns what it found, with an error when history.jsonl is there but
// can't be read to its end: a missed /login merges two stretches, which then
// name fewer rows, and the ledger keeps its marks after Claude Code trims the
// file. A line longer than historyLine holds a large paste, never a /login,
// and is passed over without stopping the read.
func (claude) Marks(home string) ([]AccountMark, error) {
	var marks []AccountMark
	for _, acct := range claudeSignIns(home) {
		if acct.AccountUUID != "" {
			marks = append(marks, AccountMark{
				Kind: MarkPair, Account: acct.AccountUUID, Org: acct.OrganizationUUID,
			})
		}
	}
	path := filepath.Join(toolconfig.ClaudeDir(home), "history.jsonl")
	file, err := os.Open(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return marks, nil
	case err != nil:
		return marks, fmt.Errorf("can't read %w", operation.FileError(path, err))
	}
	defer func() { _ = file.Close() }()
	lines := newLineReader(context.Background(), file, historyLine)
	for {
		line, err := lines.next()
		if login, ok := loginMark(line); ok {
			marks = append(marks, login)
		}
		switch {
		case errors.Is(err, io.EOF):
			return marks, nil
		case err != nil:
			return marks, fmt.Errorf("can't read all of %w", operation.FileError(path, err))
		}
	}
}

// historyLine bounds a line of history.jsonl that Marks reads: a longer one
// is nil, which is no login mark.
const historyLine = 64 << 20

// loginMark is the mark of a line of history.jsonl that is a /login typed at
// the prompt.
func loginMark(line []byte) (AccountMark, bool) {
	if !bytes.Contains(line, []byte(`"display":"/login`)) {
		return AccountMark{}, false
	}
	var entry struct {
		Display   string `json:"display"`
		Timestamp int64  `json:"timestamp"` // milliseconds
	}
	if !decode(bytes.TrimRight(line, "\r\n"), &entry) || entry.Timestamp <= 0 {
		return AccountMark{}, false
	}
	if command, _, _ := strings.Cut(strings.TrimSpace(entry.Display), " "); command != "/login" {
		return AccountMark{}, false
	}
	return AccountMark{Kind: MarkLogin, Time: time.UnixMilli(entry.Timestamp)}, true
}

// Labels maps the subscription id of each organization a sign-in on disk
// belongs to (claudeSignIns) to its report label, the latest sign-in's.
func (claude) Labels(home string) map[string]string {
	labels := map[string]string{}
	for _, acct := range claudeSignIns(home) {
		if label := claudePlanLabel(acct); label != "" {
			labels["claude:"+acct.OrganizationUUID] = label
		}
	}
	return labels
}

// claudePlanLabel is the report label of a sign-in's plan: its
// planDisplayName when Claude Code recorded one (the name the plan is sold
// under, "Ultra" or "Max", which Claude Code reads from the account profile
// and checks before it writes it), else its organizationType without the
// "claude_" prefix and with the first letter capitalised, so claude_max is
// "Max" and claude_team is "Team". The two tiers sold as Max and Ultra are
// both claude_max, so only the display name tells them apart. An
// organization plan (claude_team, claude_enterprise) adds its organization,
// "Team (Example Org)", because many organizations share one type; any other
// type is a personal plan named by its label alone, so a consumer tier this
// code does not know yet still reads by its name. A sign-in with neither a
// type nor a display name is named by its organization.
func claudePlanLabel(acct claudeOAuth) string {
	orgType, orgName := acct.OrganizationType, acct.OrganizationName
	label := acct.PlanDisplayName
	if label == "" {
		label = strings.TrimPrefix(orgType, "claude_")
		if label != "" {
			label = strings.ToUpper(label[:1]) + label[1:]
		}
	}
	switch {
	case orgName == "":
		return label
	case orgType == "claude_team" || orgType == "claude_enterprise":
		return label + " (" + orgName + ")"
	case label == "":
		return orgName
	}
	return label
}
