# old_locations COMMAND... runs COMMAND with the owner's old-location
# variables, which the apply passes to its scripts as WORKBENCH_OLD_<NAME> so
# no installer sees them, and names in WORKBENCH_OLD_NAMES (internal/migrate
# OldLocationVariables). Only the installed workbench needs them, to find the
# files earlier releases left.
old_locations() {
    local name passed
    local assignments=()
    for name in ${WORKBENCH_OLD_NAMES:-}; do
        passed=WORKBENCH_OLD_$name
        [ -z "${!passed:-}" ] || assignments+=("$name=${!passed}")
    done
    # bash 3.2 (macOS) under set -u rejects "${a[@]}" for an empty array.
    env ${assignments[@]+"${assignments[@]}"} "$@"
}
