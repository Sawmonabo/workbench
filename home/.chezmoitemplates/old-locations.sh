# old_locations COMMAND... runs COMMAND with the owner's old-location
# variables, which the apply passes to its scripts as WORKBENCH_OLD_<NAME> so
# no installer sees them (internal/migrate OldLocationVariables; keep the two
# lists the same). Only the installed workbench needs them, to find the files
# earlier releases left.
old_locations() {
    local name passed
    local assignments=()
    for name in XDG_DATA_HOME XDG_STATE_HOME XDG_CONFIG_HOME XDG_CACHE_HOME \
        CLAUDE_COSTS_LEDGER CLAUDE_COSTS_STATE CLAUDE_COSTS_RATES CLAUDE_COSTS_CACHE; do
        passed=WORKBENCH_OLD_$name
        [ -z "${!passed:-}" ] || assignments+=("$name=${!passed}")
    done
    # bash 3.2 (macOS) under set -u rejects "${a[@]}" for an empty array.
    env ${assignments[@]+"${assignments[@]}"} "$@"
}
