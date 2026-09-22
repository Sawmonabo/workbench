// Package pythonpolicy embeds the portable Python project policy that project
// configure applies. It is not a public API. Its name differs from its
// directory so callers can keep local variables named python.
package pythonpolicy

import _ "embed"

// Policy is the project tooling policy (policy.toml).
//
//go:embed policy.toml
var Policy string

// Extensions is the recommended editor extension list (extensions.json).
//
//go:embed extensions.json
var Extensions []byte

// Ignore holds the .gitignore entries configure appends (gitignore.entries).
//
//go:embed gitignore.entries
var Ignore string
