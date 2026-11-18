// Package rustpolicy embeds the portable Rust project policy that project
// apply applies. It is not a public API.
package rustpolicy

import _ "embed"

// ID names the policy; the manifest's Workbench record and the CI job name
// record it.
const ID = "rust-v1"

// Lints are the lint levels merged into a single crate's manifest
// (lints.toml); a workspace root gets them under [workspace.lints].
//
//go:embed lints.toml
var Lints string

// Profile is the dev profile setting merged into the root manifest
// (profile.toml).
//
//go:embed profile.toml
var Profile string

// Rustfmt is the rustfmt configuration written when the project has none
// (rustfmt.toml); EDITION stands for the manifest's edition.
//
//go:embed rustfmt.toml
var Rustfmt string

// Deny is the cargo-deny configuration written when the project has none
// (deny.toml).
//
//go:embed deny.toml
var Deny []byte

// Mbx is the mbx workspace setting written when the project has none
// (mbx.toml).
//
//go:embed mbx.toml
var Mbx []byte

// Extensions is the recommended editor extension list (extensions.json).
//
//go:embed extensions.json
var Extensions []byte

// Ignore holds the .gitignore entries --gitignore appends (gitignore.entries).
//
//go:embed gitignore.entries
var Ignore string
