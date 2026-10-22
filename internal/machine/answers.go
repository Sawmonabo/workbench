package machine

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
	"github.com/pelletier/go-toml/v2"
)

// Answers is a machine's validated [data] table, the native chezmoi template
// data every render uses.
type Answers map[string]any

func parseAnswers(c operation.Context, raw []byte) (Answers, error) {
	answers, err := readAnswers(raw)
	if err != nil {
		return nil, err
	}
	return answers, validateAnswers(c, answers)
}

// ReadAnswers reads and validates the answers saved in c.Native.Config, for an
// apply step that runs inside the apply that checked them.
func ReadAnswers(c operation.Context) (Answers, error) {
	raw, err := operation.ReadPrivateInput(c.Native.Config, 1<<20)
	if err != nil {
		return nil, err
	}
	return parseAnswers(c, raw)
}

// readAnswers reads a [data]-only machine config without checking that it
// answers every question; the questionnaire can complete it. It leaves out
// the dropped answers, so they never block a run.
func readAnswers(raw []byte) (Answers, error) {
	answers, err := decodeAnswers(raw)
	for _, key := range droppedAnswers {
		delete(answers, key)
	}
	return answers, err
}

// decodeAnswers is readAnswers keeping the dropped answers, for the writer
// that has to see them to drop them.
func decodeAnswers(raw []byte) (Answers, error) {
	var config map[string]any
	if err := toml.Unmarshal(raw, &config); err != nil {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Machine answers are not valid TOML; original input retained",
		)
	}
	if config["data"] == nil {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Machine config must contain a [data] table; adopt answers without native hooks or commands",
		)
	}
	for key := range config {
		if key != "data" && key != "effects" {
			return nil, operation.Fail(
				operation.ExitInvalid,
				"answers",
				"Machine config may contain only [data] and [effects] tables; adopt answers without native hooks or commands",
			)
		}
	}
	data, ok := config["data"].(map[string]any)
	if !ok {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Machine [data] must be a table",
		)
	}
	return Answers(data), nil
}

// derivedAnswers are the answers the questionnaire derives from others rather
// than asks.
var derivedAnswers = []string{"has_work", "has_personal", "is_wsl"}

// retiredAnswers are the work tokens the questionnaire once asked. Migration
// 0005 moves them into work.env; until it has, they stay accepted and redacted
// but are never asked, and every write of the answers keeps them.
var retiredAnswers = []string{"jira_api_token", "gitlab_token"}

// droppedAnswers are answers an earlier release saved that nothing uses now:
// restart_wsl_path, the folder of the RestartWSL helpers Workbench no longer
// writes. Never asked or carried, they are dropped the next time a change to
// the answers is written.
var droppedAnswers = []string{"restart_wsl_path"}

// carryRetired copies into answers each retired answer saved holds and
// answers lacks.
func carryRetired(answers, saved Answers) {
	for _, key := range retiredAnswers {
		if value, ok := saved[key]; ok {
			if _, has := answers[key]; !has {
				answers[key] = value
			}
		}
	}
}

// CheckAsk reports whether every key in ask is a saved answer that the
// questionnaire asks, so setup can drop it from the seed and ask it again.
func CheckAsk(c operation.Context, ask []string) error {
	if len(ask) == 0 {
		return nil
	}
	askable, err := Askable(c)
	if err != nil {
		return err
	}
	for _, key := range ask {
		if !slices.Contains(askable, key) {
			return operation.Fail(
				operation.ExitInvalid,
				"ask",
				fmt.Sprintf(
					"%q is not one of this machine's answers; --ask takes %s",
					key,
					strings.Join(askable, ", "),
				),
			)
		}
	}
	return nil
}

// Askable lists, sorted, the saved answers --ask can ask again: every saved
// answer except the derived and retired ones.
func Askable(c operation.Context) ([]string, error) {
	raw, err := operation.ReadPrivateInput(c.Native.Config, 1<<20)
	if err != nil {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"ask",
			"--ask changes saved answers, and this machine has none; "+
				c.WorkbenchCommand()+" apply asks every question",
		)
	}
	answers, err := readAnswers(raw)
	if err != nil {
		return nil, err
	}
	var askable []string
	for key := range answers {
		if !slices.Contains(derivedAnswers, key) && !slices.Contains(retiredAnswers, key) {
			askable = append(askable, key)
		}
	}
	slices.Sort(askable)
	return askable, nil
}

// validateAnswers is also the post-init gate. It never silently defaults missing
// unattended inputs or normalizes a persisted role/editor/version policy. Its
// refusal names the key, never the value, which may be a token.
func validateAnswers(c operation.Context, a Answers) error {
	if unknown := a.unknownKeys(); len(unknown) > 0 {
		return operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Machine answers have keys Workbench does not ask for: "+strings.Join(unknown, ", ")+
				"; remove them from the [data] table",
		)
	}
	if key := a.invalidKey(); key != "" {
		command := c.WorkbenchCommand()
		return operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Machine answer "+key+" is missing or invalid; "+command+" apply at a terminal, "+
				"without --local-build, asks the missing ones, or save them with "+command+
				" init --answers-from FILE",
		)
	}
	return nil
}

// answerKeys are every key the questionnaire can save, and the retired ones.
var answerKeys = []string{
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
	"work_folder",
	"personal_folder",
	"jira_url",
	"gitlab_url",
	"dispatch_folder",
	"jira_api_token",
	"gitlab_token",
	"wsl_memory",
	"wsl_processors",
	"wsl_swap",
	"terminal_theme",
}

// unknownKeys are the keys the questionnaire never saves, sorted.
func (a Answers) unknownKeys() []string {
	var unknown []string
	for key := range a {
		if !slices.Contains(answerKeys, key) {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)
	return unknown
}

func (a Answers) text(key string) string { value, _ := a[key].(string); return value }

// invalidKey is the first answer, in question order, that is missing or invalid,
// or "" when every answer this machine needs is valid.
func (a Answers) invalidKey() string {
	choices := []struct {
		key     string
		allowed []string
	}{
		{"machine_role", []string{"personal", "work", "both"}},
		{"editor", []string{"code", "vim"}},
		{"versions_mode", []string{"pinned", "latest"}},
	}
	for _, choice := range choices {
		if !slices.Contains(choice.allowed, a.text(choice.key)) {
			return choice.key
		}
	}
	role := a.text("machine_role")
	work, personal := role != "personal", role != "work"
	for i, want := range []bool{work, personal, platform.WSL()} {
		if key := derivedAnswers[i]; a[key] != want {
			return key
		}
	}
	identity := []string{"name", "email"}
	if role == "both" {
		identity = append(identity, "personal_email", "work_email")
	}
	for _, key := range identity {
		value := a.text(key)
		if value == "" || strings.ContainsFunc(value, unicode.IsControl) {
			return key
		}
	}
	if key := a.invalidFolderKey(work, personal); key != "" {
		return key
	}
	if key := a.invalidWorkKey(work); key != "" {
		return key
	}
	if platform.WSL() {
		return a.invalidWSLKey()
	}
	return ""
}

// folderPattern is a role folder as saved: home-relative text such as ~/name,
// below the home folder and never the home folder itself.
var folderPattern = regexp.MustCompile(`^~(/[A-Za-z0-9._-]+)+$`)

// validFolder reports whether value is a role folder: the pattern with no "."
// or ".." part. A trailing "/" is not valid; the questionnaire drops it first.
func validFolder(value string) bool {
	return folderPattern.MatchString(value) &&
		!slices.ContainsFunc(strings.Split(value, "/"), func(part string) bool {
			return part == "." || part == ".."
		})
}

// invalidFolderKey is the first folder, work then personal, that a role this
// machine has leaves missing or invalid; on both roles, personal_folder when
// the two are equal or one contains the other; otherwise "".
func (a Answers) invalidFolderKey(work, personal bool) string {
	workFolder, personalFolder := a.text("work_folder"), a.text("personal_folder")
	switch {
	case work && !validFolder(workFolder):
		return "work_folder"
	case personal && !validFolder(personalFolder):
		return "personal_folder"
	case work && personal && (workFolder == personalFolder ||
		strings.HasPrefix(personalFolder, workFolder+"/") ||
		strings.HasPrefix(workFolder, personalFolder+"/")):
		return "personal_folder"
	}
	return ""
}

// workURLPattern is a work tool's address as saved: https, no space or
// control character, and no empty part or trailing "/", which the
// questionnaire drops first.
var workURLPattern = regexp.MustCompile(
	`^https://[^/[:space:][:cntrl:]]+(/[^/[:space:][:cntrl:]]+)*$`,
)

// invalidWorkKey is the first work answer, the Jira and GitLab addresses then
// the dispatch servers' checkout, that a machine with the work role leaves
// missing or invalid; otherwise "". The checkout is one folder name in the
// work folder.
func (a Answers) invalidWorkKey(work bool) string {
	if !work {
		return ""
	}
	for _, key := range []string{"jira_url", "gitlab_url"} {
		if !workURLPattern.MatchString(a.text(key)) {
			return key
		}
	}
	if checkout := a.text(
		"dispatch_folder",
	); !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(checkout) ||
		checkout == "." ||
		checkout == ".." {
		return "dispatch_folder"
	}
	return ""
}

// folder is the absolute path of the role folder saved under key, below home,
// or "" when the key is absent or not a valid folder.
func (a Answers) folder(home, key string) string {
	value := a.text(key)
	if !validFolder(value) {
		return ""
	}
	return filepath.Join(home, strings.TrimPrefix(value, "~/"))
}

// RoleFolders are the role folders saved in <config>/machine.toml as absolute
// paths under home, personal first. They are nil when there is no
// machine.toml or it saves none, and err says a machine.toml that is there
// can't be read. They never name a default: the one fallback is the costs
// report's.
func RoleFolders(home, config string) ([]string, error) {
	path := filepath.Join(config, "machine.toml")
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	raw, err := operation.ReadPrivateInput(path, 1<<20)
	if err != nil {
		return nil, err
	}
	answers, err := readAnswers(raw)
	if err != nil {
		return nil, err
	}
	var folders []string
	for _, key := range []string{"personal_folder", "work_folder"} {
		if folder := answers.folder(home, key); folder != "" {
			folders = append(folders, folder)
		}
	}
	return folders, nil
}

// invalidWSLKey is the first WSL answer, the sizing then Windows Terminal's
// color scheme, that is missing or invalid, or "".
func (a Answers) invalidWSLKey() string {
	if !regexp.MustCompile(`^[1-9][0-9]{0,6}(MB|GB)$`).MatchString(a.text("wsl_memory")) {
		return "wsl_memory"
	}
	if !regexp.MustCompile(`^(0|[1-9][0-9]{0,6}(MB|GB))$`).MatchString(a.text("wsl_swap")) {
		return "wsl_swap"
	}
	processors, ok := a["wsl_processors"].(int64)
	if !ok || processors < 1 || processors > 1024 {
		return "wsl_processors"
	}
	if !slices.Contains([]string{"kanagawa-dragon", "catppuccin-mocha"}, a.text("terminal_theme")) {
		return "terminal_theme"
	}
	return ""
}

func (a Answers) secrets() []string {
	var values []string
	for _, key := range retiredAnswers {
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
// compatibility reader: apply reads only machine.toml. It returns the
// encoded answers to write, with the retired answers machine.toml holds; a
// machine.toml whose answers can't be read is refused, never replaced.
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
		return plan, nil, operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Existing chezmoi config is not valid TOML",
		)
	}
	data, ok := config["data"].(map[string]any)
	if !ok {
		return plan, nil, operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Existing chezmoi config has no [data] table",
		)
	}
	answers := Answers(data)
	if err = validateAnswers(c, answers); err != nil {
		return plan, nil, err
	}
	target := filepath.Join(c.Paths.Config, "machine.toml")
	selection, err := ReadSelection(target)
	if err != nil {
		return plan, nil, err
	}
	var existing []byte
	if _, statErr := os.Lstat(target); statErr == nil {
		if existing, err = operation.ReadPrivateInput(target, 1<<20); err != nil {
			return plan, nil, err
		}
		saved, readErr := readAnswers(existing)
		if readErr != nil {
			return plan, nil, readErr
		}
		carryRetired(answers, saved)
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return plan, nil, statErr
	}
	encoded, err := encodeMachineConfig(answers, selection)
	if err != nil {
		return plan, nil, err
	}
	previous, action := "absent", "create"
	if existing != nil {
		previous, action = operation.SHA256Hex(existing), "modify"
		if bytes.Equal(existing, encoded) {
			action = ""
		}
	}
	plan.Inputs = []operation.Input{
		{Name: "adopted-answers", Digest: operation.SHA256Hex(encoded)},
		{Name: "machine-answers", Digest: previous},
	}
	if action != "" {
		description := fmt.Sprintf(
			"Save machine answers (%s) from the [data] table of %s; its other keys are ignored",
			answers.label(),
			from,
		)
		plan.Edits = []operation.Edit{{Path: target, Action: action, Description: description}}
	}
	plan.Complete = true
	return plan, encoded, nil
}
