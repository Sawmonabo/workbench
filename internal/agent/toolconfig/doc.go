// Package toolconfig reads the AI coding tools' own config: where Claude Code
// and Codex keep their config folders, Claude Code's settings files, a
// variable that moves a tool's config folder away from the one Workbench
// installs in, and whether a tool runs the hooks Workbench installs. It reads
// those files and never writes them.
package toolconfig
