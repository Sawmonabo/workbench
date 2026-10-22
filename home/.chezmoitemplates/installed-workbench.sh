# The installed workbench, in the bin folder the apply resolved: the one the
# Claude Code and Codex hooks run (the workbench-command template).
workbench="${WORKBENCH_BIN_DIR:-$HOME/.local/bin}/workbench"
# knows_command NAME: the installed workbench has the NAME command. An older
# one answers `NAME --help` with its general help, which has no NAME usage
# line, and exits 0.
knows_command() {
    local usage
    usage=$("$workbench" "$1" --help 2>/dev/null || true)
    [[ $usage == *"workbench $1 ["* ]]
}
