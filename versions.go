// Package workbench builds the management version pins into the executable.
// It sits at the module root because Go embeds only files at or below the
// embedding package's directory.
package workbench

import _ "embed"

// Versions is home/.chezmoidata/versions.toml as it was when the executable
// was built.
//
//go:embed home/.chezmoidata/versions.toml
var Versions []byte
