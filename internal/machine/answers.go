package machine

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/pelletier/go-toml/v2"
)

// Answers is a machine's validated [data] table, the native chezmoi template
// data every render uses.
type Answers map[string]any

func parseAnswers(raw []byte) (Answers, error) {
	var config map[string]any
	if err := toml.Unmarshal(raw, &config); err != nil {
		return nil, operation.Fail(
			2,
			"answers",
			"Machine answers are not valid TOML; original input retained",
		)
	}
	if len(config) != 1 || config["data"] == nil {
		return nil, operation.Fail(
			2,
			"answers",
			"Machine config must contain only a [data] table; adopt answers without native hooks or commands",
		)
	}
	data, ok := config["data"].(map[string]any)
	if !ok {
		return nil, operation.Fail(2, "answers", "Machine [data] must be a table")
	}
	answers := Answers(data)
	return answers, validateAnswers(answers)
}

// validateAnswers is also the post-init gate. It never silently defaults missing
// unattended inputs or normalizes a persisted role/editor/version policy.
func validateAnswers(a Answers) error {
	fail := func() error {
		return operation.Fail(
			2,
			"answers",
			"Incomplete or invalid machine answers; use the native setup questionnaire",
		)
	}
	text := func(key string) string { value, _ := a[key].(string); return value }
	role := text("machine_role")
	if !slices.Contains([]string{"personal", "work", "both"}, role) ||
		!slices.Contains([]string{"code", "vim"}, text("editor")) ||
		!slices.Contains([]string{"pinned", "latest"}, text("versions_mode")) {
		return fail()
	}
	work, personal := role != "personal", role != "work"
	if a["has_work"] != work || a["has_personal"] != personal || a["is_wsl"] != isWSL() {
		return fail()
	}
	identity := []string{"name", "email"}
	if role == "both" {
		identity = append(identity, "personal_email", "work_email")
	}
	for _, key := range identity {
		value := text(key)
		if value == "" || strings.ContainsFunc(value, unicode.IsControl) {
			return fail()
		}
	}
	if work {
		for _, key := range []string{"jira_api_token", "gitlab_token"} {
			if _, ok := a[key].(string); !ok {
				return fail()
			}
		}
	}
	if isWSL() {
		if !regexp.MustCompile(`^[1-9][0-9]{0,6}(MB|GB)$`).MatchString(text("wsl_memory")) ||
			!regexp.MustCompile(`^(0|[1-9][0-9]{0,6}(MB|GB))$`).MatchString(text("wsl_swap")) {
			return fail()
		}
		processors, ok := a["wsl_processors"].(int64)
		if !ok || processors < 1 || processors > 1024 {
			return fail()
		}
		path := text("restart_wsl_path")
		if path == "" || strings.ContainsFunc(path, unicode.IsControl) ||
			slices.Contains(
				strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }),
				"..",
			) {
			return fail()
		}
	}
	allowed := []string{
		"name",
		"email",
		"machine_role",
		"has_work",
		"has_personal",
		"is_wsl",
		"editor",
		"versions_mode",
		"personal_email",
		"work_email",
		"jira_api_token",
		"gitlab_token",
		"wsl_memory",
		"wsl_processors",
		"wsl_swap",
		"restart_wsl_path",
	}
	for key := range a {
		if !slices.Contains(allowed, key) {
			return fail()
		}
	}
	return nil
}

func (a Answers) secrets() []string {
	var values []string
	for _, key := range []string{"jira_api_token", "gitlab_token"} {
		if value, ok := a[key].(string); ok && value != "" {
			values = append(values, value)
		}
	}
	return values
}

func (a Answers) label() string { return fmt.Sprintf("%s/%s", a["machine_role"], a["versions_mode"]) }

// AdoptionPlan previews saving the [data] table of an existing native chezmoi
// config, such as dotfiles' ~/.config/chezmoi/chezmoi.toml, as Workbench's
// machine answers. Other keys (sourceDir, hooks, commands) are ignored and
// never run. This is the design's one-time answer adoption, not a
// compatibility reader: plan and apply read only machine.toml. It returns the
// encoded answers to write.
func AdoptionPlan(c operation.Context, from string) (operation.Plan, []byte, error) {
	plan := operation.Plan{
		Scope: c.Scope,
		RecoveryLimits: []string{
			"machine.toml is not checkpointed; the source config is left unchanged",
		},
	}
	raw, err := operation.ReadPrivateInput(from, 1<<20)
	if err != nil {
		return plan, nil, err
	}
	var config map[string]any
	if toml.Unmarshal(raw, &config) != nil {
		return plan, nil, operation.Fail(2, "answers", "Existing chezmoi config is not valid TOML")
	}
	data, ok := config["data"].(map[string]any)
	if !ok {
		return plan, nil, operation.Fail(
			2,
			"answers",
			"Existing chezmoi config has no [data] table",
		)
	}
	answers := Answers(data)
	if err = validateAnswers(answers); err != nil {
		return plan, nil, err
	}
	encoded, err := toml.Marshal(map[string]any{"data": answers})
	if err != nil {
		return plan, nil, err
	}
	target := filepath.Join(c.Paths.Config, "machine.toml")
	previous, action := "absent", "create"
	if _, statErr := os.Lstat(target); statErr == nil {
		existing, readErr := operation.ReadPrivateInput(target, 1<<20)
		if readErr != nil {
			return plan, nil, readErr
		}
		previous, action = digest(existing), "modify"
		if bytes.Equal(existing, encoded) {
			action = ""
		}
	} else if !os.IsNotExist(statErr) {
		return plan, nil, statErr
	}
	plan.Inputs = []operation.Input{
		{Name: "adopted-answers", Digest: digest(encoded)},
		{Name: "machine-answers", Digest: previous},
	}
	if action != "" {
		plan.Edits = []operation.Edit{
			{
				Path:        target,
				Action:      action,
				Description: "Save machine answers (" + answers.label() + ") from the [data] table of " + from + "; its other keys are ignored",
			},
		}
	}
	plan.Complete = true
	return plan, encoded, nil
}
