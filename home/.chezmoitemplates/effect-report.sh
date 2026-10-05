# part_blocked EFFECT REASON says that one effect could not be done, without
# stopping the rest of the script. It is named on stderr and in the file
# Workbench reads ($WORKBENCH_EFFECT_REPORT, one "effect, tab, reason" line
# each, all shown when an effect has several) to show it on that effect's result
# line. REASON is one line without a tab. A script that runs on every apply
# (run_after_, run_before_) may then end 0: it tries again next time anyway.
part_blocked() {
    echo "WORKBENCH_EFFECT $1 blocked: $2" >&2
    if [ -n "${WORKBENCH_EFFECT_REPORT:-}" ]; then
        printf '%s\t%s\n' "$1" "$2" >>"$WORKBENCH_EFFECT_REPORT"
    fi
}

# part_failed EFFECT REASON is part_blocked for a script that then ends nonzero:
# `part_failed EFFECT REASON || status=1`, then `exit "$status"`. chezmoi runs a
# run_once_ or run_onchange_ script that ended nonzero again on the next apply,
# and Workbench runs chezmoi with --keep-going, so the scripts after it still
# run.
part_failed() {
    part_blocked "$@"
    return 1
}
