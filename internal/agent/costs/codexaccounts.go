package costs

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
)

// SignIn is the sign-in Codex keeps in auth.json: the email and plan claims of
// its ID token. The subscription is "codex:<plan_type>" and the label the plan
// as shown ("Plus", "Pro Lite"); the account id only resolves an email (see
// Accounts), so one plan is one group. An API-key sign-in is the subscription
// "api-key" with no email. Anything unreadable is the account "unknown" with no
// subscription.
func (codex) SignIn(home string) SignIn {
	auth, ok := readCodexAuth(filepath.Join(toolconfig.CodexDir(home), "auth.json"))
	if !ok {
		return SignIn{Account: "unknown"}
	}
	if auth.apiKey {
		return SignIn{Account: "unknown", Subscription: "api-key", Label: "API key"}
	}
	out := SignIn{Account: firstNonEmpty(auth.email, "unknown")}
	if auth.plan != "" {
		out.Subscription = "codex:" + auth.plan
		out.Label = codexPlanLabel(auth.plan)
	}
	return out
}

// Accounts maps each Codex sign-in's chatgpt_account_id to its email: auth.json
// and every agent-overflow-accounts/*/auth.json under CODEX_HOME, ID token
// payloads only. A rollout's creator_account_id names the account that ran it.
// An id that two sign-ins claim with different emails is left out rather than
// guessed, so its rows fall back to the session and observed evidence.
func (codex) Accounts(home string) map[string]string {
	dir := toolconfig.CodexDir(home)
	paths := []string{filepath.Join(dir, "auth.json")}
	extra, _ := filepath.Glob(filepath.Join(dir, "agent-overflow-accounts", "*", "auth.json"))
	sort.Strings(extra)
	paths = append(paths, extra...)
	accounts := map[string]string{}
	ambiguous := map[string]bool{}
	for _, path := range paths {
		auth, ok := readCodexAuth(path)
		if !ok || auth.accountID == "" || auth.email == "" {
			continue
		}
		if prev, seen := accounts[auth.accountID]; seen && prev != auth.email {
			ambiguous[auth.accountID] = true
		}
		accounts[auth.accountID] = auth.email
	}
	for id := range ambiguous {
		delete(accounts, id)
	}
	return accounts
}

// codexPlanLabel is a plan_type as the report shows it: prolite is "Pro Lite",
// anything else its value capitalized ("plus" is "Plus").
func codexPlanLabel(plan string) string {
	if plan == "prolite" {
		return "Pro Lite"
	}
	if plan == "" {
		return ""
	}
	return strings.ToUpper(plan[:1]) + plan[1:]
}

// codexAuth is what one auth.json says about its sign-in.
type codexAuth struct {
	email, plan, accountID string
	apiKey                 bool // an API-key sign-in: no ChatGPT account behind it
}

// readCodexAuth reads an auth.json. The ID token is only decoded, never
// verified; the access and refresh tokens are never read. It is false when the
// file or its ID token cannot be read and no API key is configured.
func readCodexAuth(path string) (codexAuth, bool) {
	var auth struct {
		Mode   string `json:"auth_mode"`
		Key    string `json:"OPENAI_API_KEY"`
		Tokens struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	raw, err := os.ReadFile(path)
	if err != nil || !decode(raw, &auth) {
		return codexAuth{}, false
	}
	if auth.Tokens.IDToken == "" {
		apiKey := auth.Key != "" || strings.EqualFold(auth.Mode, "apikey")
		return codexAuth{apiKey: apiKey}, apiKey
	}
	parts := strings.Split(auth.Tokens.IDToken, ".")
	if len(parts) != 3 {
		return codexAuth{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return codexAuth{}, false
	}
	var claims struct {
		Email   string `json:"email"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
		Auth struct {
			Plan      string `json:"chatgpt_plan_type"`
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if !decode(payload, &claims) {
		return codexAuth{}, false
	}
	return codexAuth{
		email:     firstNonEmpty(claims.Email, claims.Profile.Email),
		plan:      strings.ToLower(claims.Auth.Plan),
		accountID: claims.Auth.AccountID,
	}, true
}
