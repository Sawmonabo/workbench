// Package pythonpolicy embeds the portable Python project policy that project
// apply applies. It is not a public API.
package pythonpolicy

import _ "embed"

// ID names the policy; project provenance and the CI job name record it.
const ID = "python-v1"

// Policy is the project tooling policy (policy.toml).
//
//go:embed policy.toml
var Policy string

// Extensions is the recommended editor extension list (extensions.json).
//
//go:embed extensions.json
var Extensions []byte

// Ignore holds the .gitignore entries --gitignore appends (gitignore.entries).
//
//go:embed gitignore.entries
var Ignore string

// Hooks holds the policy's lefthook jobs (lefthook.yml), which --hooks
// combines with the other selected languages' jobs into one lefthook.yml. The
// pre-push test job is written only when the dev group lists pytest.
//
//go:embed lefthook.yml
var Hooks []byte

// Ecosystem is the Dependabot package-ecosystem --dependabot lists for the
// policy's manifests and lockfile.
const Ecosystem = "uv"
