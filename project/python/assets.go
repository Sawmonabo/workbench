// Package pythonpolicy contains portable policy assets, not application templates.
package pythonpolicy

import _ "embed"

//go:embed policy.toml
var Policy string

//go:embed extensions.json
var Extensions []byte

//go:embed gitignore.entries
var Ignore string
