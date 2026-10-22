package machine

import (
	"testing"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// TestRetiredTokensSurviveEveryAnswerWrite guards the owner's saved work
// tokens until migration 0005 moves them into work.env: the questionnaire no
// longer asks for them, so a re-ask or an adopted answers file that lacks them
// would drop the only copy Workbench holds, and nothing could recover it.
func TestRetiredTokensSurviveEveryAnswerWrite(t *testing.T) {
	saved := Answers{"jira_api_token": "synthetic-jira", "gitlab_token": "synthetic-gitlab"}
	answers := Answers{
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
}
