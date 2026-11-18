// Package gopolicy embeds the portable Go project policy that project apply
// applies. It is not a public API.
package gopolicy

import _ "embed"

// ID names the policy; the project's .workbench.toml record and the CI job
// name record it.
const ID = "go-v1"

// Golangci is the golangci-lint configuration written when the module has none
// (golangci.yml).
//
//go:embed golangci.yml
var Golangci []byte

// Extensions is the recommended editor extension list (extensions.json).
//
//go:embed extensions.json
var Extensions []byte

// Ignore holds the .gitignore entries --gitignore appends (gitignore.entries).
//
//go:embed gitignore.entries
var Ignore string

// RecordHeader is the comment that opens the .workbench.toml record
// (record.toml); Workbench writes the record's keys after it.
//
//go:embed record.toml
var RecordHeader string
