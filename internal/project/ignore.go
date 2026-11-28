package project

import (
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/project/gopolicy"
	"github.com/Sawmonabo/workbench/project/rustpolicy"
)

// pythonIgnore is the part of pythonpolicy.Ignore --gitignore appends. Test,
// distribution, coverage and secret-file patterns are optional assets; their
// relevance cannot be inferred merely from Python ownership.
var pythonIgnore = []string{".venv/", "__pycache__/", "*.py[cod]", ".ruff_cache/"}

// ignoreEntries returns the entries --gitignore appends for languages, each
// once, in the order the languages are configured.
func ignoreEntries(languages []string) []string {
	var entries []string
	for _, language := range configurable {
		if !slices.Contains(languages, language) {
			continue
		}
		add := pythonIgnore
		switch language {
		case "go":
			add = strings.Split(strings.TrimSpace(gopolicy.Ignore), "\n")
		case "rust":
			add = strings.Split(strings.TrimSpace(rustpolicy.Ignore), "\n")
		}
		for _, entry := range add {
			if !slices.Contains(entries, entry) {
				entries = append(entries, entry)
			}
		}
	}
	return entries
}

// ignoreDescription describes the --gitignore edit for languages.
func ignoreDescription(languages []string) string {
	switch {
	case slices.Equal(languages, []string{"python"}):
		return "Append relevant Python environment/cache ignore entries preserving order"
	case slices.Equal(languages, []string{"go"}):
		return "Append Go build and test output ignore entries preserving order"
	case slices.Equal(languages, []string{"rust"}):
		return "Append the Rust build output ignore entry preserving order"
	case slices.Equal(languages, []string{"go", "python"}):
		return "Append relevant Python environment/cache and Go build output ignore entries preserving order"
	}
	parts := map[string]string{
		"go":     "Go build output",
		"python": "Python environment/cache",
		"rust":   "Rust build output",
	}
	var named []string
	for _, language := range []string{"python", "go", "rust"} {
		if slices.Contains(languages, language) {
			named = append(named, parts[language])
		}
	}
	return "Append relevant " + display.List(named) + " ignore entries preserving order"
}

// knownNegations are the negation lines no appended entry can change the
// meaning of: Python's secret-file example and the exception that lets Git
// commit the extension recommendations.
var knownNegations = []string{"!.env.example", "!.vscode/extensions.json"}

// mergeIgnore returns the edit that appends the absent entries to a
// .gitignore. Other negations may change the meaning of an appended rule, so
// a file holding one gets a proposal instead of speculative rewriting; a file
// that already holds every entry is left as it is, negations and all.
func mergeIgnore(entries []string) func([]byte) ([]byte, error) {
	return func(data []byte) ([]byte, error) {
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		var missing []string
		for _, entry := range entries {
			if !slices.Contains(lines, entry) {
				missing = append(missing, entry)
			}
		}
		if len(missing) == 0 {
			return data, nil
		}
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "!") &&
				!slices.Contains(knownNegations, line) {
				return nil, operation.Fail(
					operation.ExitBlocked,
					"gitignore",
					"Existing negations require review before adding ignore rules",
				)
			}
		}
		result := string(data)
		for _, entry := range missing {
			if result != "" && !strings.HasSuffix(result, "\n") {
				result += "\n"
			}
			result += entry + "\n"
		}
		return []byte(result), nil
	}
}
