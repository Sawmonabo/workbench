package machine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// TestRetiredTokensSurviveEveryAnswerWrite guards the owner's saved work
// tokens until migration 0005 moves them into work.env: the questionnaire no
// longer asks for them, so a re-ask or an adopted answers file that lacks them
// would drop the only copy Workbench holds, and nothing could recover it. A
// saved file the answers check rejects is refused, never rewritten without
// them.
func TestRetiredTokensSurviveEveryAnswerWrite(t *testing.T) {
	saved := Answers{"jira_api_token": "synthetic-jira", "gitlab_token": "synthetic-gitlab"}
	answers := workAnswers()

	carryRetired(answers, saved)
	encoded, err := encodeMachineConfig(answers, Selection{})
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := readAnswers(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range saved {
		if decoded[key] != want {
			t.Errorf("%s was not carried into the written answers", key)
		}
	}
	if key := decoded.invalidKey(); key != "" {
		t.Errorf("the written answers are invalid at %s", key)
	}

	config := t.TempDir()
	from := filepath.Join(config, "answers.toml")
	adopted, err := encodeMachineConfig(workAnswers(), Selection{})
	if err != nil {
		t.Fatal(err)
	}
	rejected := "[data]\njira_api_token = 'synthetic-jira'\ngitlab_token = 'synthetic-gitlab'\n\n[hooks]\nread = 'x'\n"
	if err = os.WriteFile(from, adopted, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(
		filepath.Join(config, "machine.toml"),
		[]byte(rejected),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, encoded, err = AdoptionPlan(operation.Context{Paths: operation.Paths{Config: config}}, from)
	if err == nil {
		decoded, _ = readAnswers(encoded)
		for key, want := range saved {
			if decoded[key] != want {
				t.Errorf("init --answers-from over a rejected machine.toml drops %s", key)
			}
		}
	}
}

// workAnswers are a work machine's answers, valid on this host, without the
// retired tokens.
func workAnswers() Answers {
	return Answers{
		"machine_role":     "work",
		"editor":           "code",
		"versions_mode":    "pinned",
		"has_work":         true,
		"has_personal":     false,
		"is_wsl":           platform.WSL(),
		"name":             "Test User",
		"email":            "user@example.invalid",
		"work_folder":      "~/repos",
		"wsl_memory":       "8GB",
		"wsl_swap":         "2GB",
		"wsl_processors":   int64(4),
		"restart_wsl_path": "Desktop/RestartWSL",
	}
}
