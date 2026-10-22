package machine

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// TestRetiredTokensSurviveEveryAnswerWrite guards the owner's saved work
// tokens until migration 0005 moves them into work.env: the questionnaire no
// longer asks for them, so a re-ask of the folder questions on the first apply
// after upgrading, or an adopted answers file, that lacks them would drop the
// only copy Workbench holds, and nothing could recover it. A saved file the
// answers check rejects is refused, never rewritten without them.
func TestRetiredTokensSurviveEveryAnswerWrite(t *testing.T) {
	saved := Answers{"jira_api_token": "synthetic-jira", "gitlab_token": "synthetic-gitlab"}

	// An earlier release's machine.toml: the tokens and no folder answer.
	upgraded := workAnswers()
	delete(upgraded, "work_folder")
	maps.Copy(upgraded, saved)
	before, err := encodeMachineConfig(upgraded, Selection{})
	if err != nil {
		t.Fatal(err)
	}
	machine := filepath.Join(t.TempDir(), "machine.toml")
	if err = os.WriteFile(machine, before, 0o600); err != nil {
		t.Fatal(err)
	}
	encoded, _, err := answersFile(machine, workAnswers())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := readAnswers(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range saved {
		if decoded[key] != want {
			t.Errorf("saving the re-asked answers drops %s", key)
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
		"jira_url":         "https://jira.example.invalid",
		"gitlab_url":       "https://gitlab.example.invalid/group",
		"dispatch_folder":  "dispatch",
		"wsl_memory":       "8GB",
		"wsl_swap":         "2GB",
		"wsl_processors":   int64(4),
		"restart_wsl_path": "Desktop/RestartWSL",
	}
}
