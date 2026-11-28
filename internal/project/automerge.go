package project

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/goccy/go-yaml"
)

// autoMergeName is the workflow --dependabot writes to turn on auto-merge for
// Dependabot's patch and minor pull requests.
var autoMergeName = filepath.Join(".github", "workflows", "dependabot-auto-merge.yml")

// The marks of a job that already merges Dependabot's pull requests: a
// condition that selects Dependabot (not one that leaves it out) or the
// fetch-metadata action, with a merge call or a general auto-merge action;
// or an action made to merge Dependabot's pull requests.
var (
	selectsDependabot = regexp.MustCompile(
		`(?i)==\s*['"]dependabot\[bot\]['"]|['"]dependabot\[bot\]['"]\s*==|dependabot/fetch-metadata|` +
			`startsWith\(\s*github\.head_ref\s*,\s*['"]dependabot/`,
	)
	mergesPullRequest = regexp.MustCompile(
		`(?i)gh\s+pr\s+merge\b|enablePullRequestAutoMerge|mergePullRequest|pulls\.merge\b|` +
			`uses: (pascalgn/automerge-action|peter-evans/enable-pull-request-automerge)@`,
	)
	mergesDependabot = regexp.MustCompile(
		`(?i)uses: (fastify/github-action-merge-dependabot|ahmadnassri/action-dependabot-auto-merge)@`,
	)
	negatedCondition = regexp.MustCompile(`!\s*\([^()]*\)`)
)

// autoMergeElsewhere reports whether a job of the workflow already merges
// Dependabot's pull requests, at once or by auto-merge. Each job is read by
// its values, so comments and other jobs do not count; a workflow that is not
// valid YAML is read as one text.
func autoMergeElsewhere(workflow []byte) bool {
	var document struct {
		Jobs map[string]any `yaml:"jobs"`
	}
	texts := []string{string(workflow)}
	if yaml.Unmarshal(workflow, &document) == nil {
		texts = nil
		for _, job := range document.Jobs {
			texts = append(texts, yamlText(job))
		}
	}
	return slices.ContainsFunc(texts, func(text string) bool {
		text = negatedCondition.ReplaceAllString(text, "")
		return mergesDependabot.MatchString(text) ||
			(selectsDependabot.MatchString(text) && mergesPullRequest.MatchString(text))
	})
}

// yamlText writes a decoded YAML value as lines of "key: value", so a text
// pattern sees each value beside its key, however the file laid it out.
func yamlText(value any) string {
	var text strings.Builder
	var write func(value any)
	write = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, member := range value {
				switch member.(type) {
				case map[string]any, []any:
					text.WriteString(key + ":\n")
					write(member)
				default:
					fmt.Fprintf(&text, "%s: %v\n", key, member)
				}
			}
		case []any:
			for _, member := range value {
				write(member)
			}
		default:
			fmt.Fprintf(&text, "%v\n", value)
		}
	}
	write(value)
	return text.String()
}

// addAutoMerge plans the workflow that turns on GitHub auto-merge for
// Dependabot's patch and minor pull requests, unless the repository has it
// (named .yml or .yaml) or another workflow that does the same, which is kept
// and reported.
func (p *Proposal) addAutoMerge(c operation.Context, ids []string) error {
	root := c.Scope.Root
	path := filepath.Join(root, autoMergeName)
	requirements, err := machine.ManagementRequirements(c)
	if err != nil {
		return err
	}
	fetchMetadata, err := requirements.Uses("dependabot/fetch-metadata")
	if err != nil {
		return err
	}
	data := renderAutoMerge(fetchMetadata, ids)
	workflows, _ := workflowFiles(root)
	for _, workflow := range workflows {
		existing, err := p.readInput(workflow)
		if err != nil {
			return err
		}
		switch {
		case workflow == path || workflow == strings.TrimSuffix(path, ".yml")+".yaml":
			p.keep(root, workflow, autoMergeName, data, "Dependabot auto-merge")
			return nil
		case autoMergeElsewhere(existing):
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				operation.Warn(
					"Kept the project's own Dependabot auto-merge configuration: "+workflow,
				),
			)
			return nil
		}
	}
	if _, err = p.readInput(path); err != nil {
		return err
	}
	if err = p.add(
		c,
		path,
		data,
		"Create the workflow that turns on auto-merge for Dependabot's patch and minor pull requests only",
	); err != nil {
		return err
	}
	if p.change(path) == nil {
		return nil
	}
	p.Plan.Warnings = append(
		p.Plan.Warnings,
		operation.Warn(
			"Dependabot auto-merge works on GitHub only",
			operation.Line{
				Label: "best with",
				Text:  "\"Allow auto-merge\" on and a default-branch rule requiring the CI check",
			},
			operation.Line{Label: "not set", Text: "Workbench sets neither"},
			operation.Line{
				Label: "no required check",
				Text:  "it waits up to 40 minutes for the other checks, then merges if one ran and none failed",
			},
			operation.Line{Label: "no CI check", Text: "Dependabot pull requests stay open"},
			operation.FixText("after a failed or slow check, run the workflow's job again"),
		),
	)
	p.Plan.Effects = append(p.Plan.Effects, operation.Effect{
		Name:        "dependabot-auto-merge",
		Description: "GitHub squash-merges Dependabot's patch and minor pull requests without review once their checks pass",
		Privilege:   "CI runner",
		Recovery:    "Restoring the workflow does not undo merges it made",
	})
	return nil
}

// renderAutoMerge writes the auto-merge workflow for the policies ids, with
// fetchMetadata, the pinned dependabot/fetch-metadata reference. Its one job
// runs only for a pull request Dependabot opened, and turns on auto-merge
// only when the update's highest change, which fetch-metadata reports for a
// group too, is a semver patch or minor.
func renderAutoMerge(fetchMetadata string, ids []string) []byte {
	return []byte(comment("Written by workbench project apply ("+strings.Join(ids, ", ")+
		"). It turns on GitHub auto-merge for Dependabot's patch and minor pull requests and "+
		"leaves every other pull request alone. Where a branch rule requires checks and \"Allow "+
		"auto-merge\" is on in the repository settings, GitHub waits for those checks itself. "+
		"Otherwise this job waits for the pull request's other checks: a failed check, or no "+
		"check at all, leaves the pull request open, and running the job again tries again. The project "+
		"owns this file; Workbench never edits an existing one.") + `name: Dependabot auto-merge
on: pull_request
permissions:
  actions: read
  checks: read
  contents: write
  pull-requests: write
  statuses: read
concurrency:
  group: dependabot-auto-merge-${{ github.event.pull_request.number }}
  cancel-in-progress: true
jobs:
  dependabot:
    if: github.event.pull_request.user.login == 'dependabot[bot]'
    runs-on: ubuntu-24.04
    timeout-minutes: 50
    env:
      PR_URL: ${{ github.event.pull_request.html_url }}
      HEAD_SHA: ${{ github.event.pull_request.head.sha }}
      HEAD_REF: ${{ github.event.pull_request.head.ref }}
      GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
    steps:
      - name: Read the Dependabot update
        id: metadata
        uses: ` + fetchMetadata + `
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}
      - name: Merge a patch or minor update once its checks pass
        if: >-
          steps.metadata.outputs.update-type == 'version-update:semver-patch' ||
          steps.metadata.outputs.update-type == 'version-update:semver-minor'
        run: |
          # Only the commit this run was started for is merged. GitHub does not delete the
          # branch of a pull request this job's token merged, so the job deletes it.
          merge() {
            gh pr merge --auto --squash --match-head-commit "$HEAD_SHA" "$PR_URL" || return
            if [ "$(gh pr view "$PR_URL" --json state --jq .state)" = MERGED ]; then
              gh api -X DELETE "repos/$GITHUB_REPOSITORY/git/refs/heads/$HEAD_REF" >/dev/null ||
                echo "The merged branch $HEAD_REF could not be deleted."
            fi
          }
          # Where a branch rule requires checks, GitHub waits for them itself.
          required=$(gh pr checks "$PR_URL" --required --json name --jq length 2>/dev/null) || required=0
          if [ "$required" -gt 0 ]; then
            if merge; then exit 0; fi
            echo "GitHub did not take the merge, so this job waits for the checks itself."
          fi
          # The state of every check but this workflow's: none, fail, pending or pass.
          others='[.[] | select(.workflow != "Dependabot auto-merge") | .bucket]
            | if length == 0 then "none"
              elif any(. == "fail" or . == "cancel") then "fail"
              elif any(. == "pending") then "pending"
              elif any(. == "pass") then "pass"
              else "none" end'
          passes=0
          missing=0
          unread=0
          errors="$RUNNER_TEMP/checks.err"
          while [ "$SECONDS" -lt 2400 ]; do
            # This job is a check itself, so a list that cannot be read is an error, not an empty one.
            state=$(gh pr checks "$PR_URL" --json bucket,workflow --jq "$others" 2>"$errors") || state=unread
            case "$state" in
              pass) passes=$((passes + 1)); missing=0; unread=0 ;;
              fail) echo "A check failed, so this pull request stays open."; exit 1 ;;
              none) passes=0; missing=$((missing + 1)); unread=0 ;;
              pending) passes=0; missing=0; unread=0 ;;
              *) passes=0; unread=$((unread + 1)) ;;
            esac
            # Two passing polls in a row, so a job that starts after another ends is seen.
            if [ "$passes" -ge 2 ]; then
              merge
              exit 0
            fi
            # Nine polls, about three minutes, without any other check: the repository runs none.
            [ "$missing" -lt 9 ] || { echo "No other check ran for this pull request, so it stays open."; exit 1; }
            # Three failed lookups in a row: the checks cannot be read.
            if [ "$unread" -ge 3 ]; then
              echo "The checks of this pull request could not be read, so it stays open:"
              cat "$errors"
              exit 1
            fi
            sleep 20
          done
          echo "The other checks did not finish in 40 minutes, so this pull request stays open."
          exit 1
`)
}
