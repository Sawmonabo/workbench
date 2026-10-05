# part_blocked EFFECT REASON says that one effect could not be done, without
# stopping the rest of the script or the scripts after it. It is named on stderr
# and in the file Workbench reads ($WORKBENCH_EFFECT_REPORT, one "effect, tab,
# reason" line each, all shown when an effect has several) to show it on that
# effect's result line. REASON is one line without a tab.
part_blocked() {
    echo "WORKBENCH_EFFECT $1 blocked: $2" >&2
    if [ -n "${WORKBENCH_EFFECT_REPORT:-}" ]; then
        printf '%s\t%s\n' "$1" "$2" >>"$WORKBENCH_EFFECT_REPORT"
    fi
}

# part_failed EFFECT REASON is part_blocked for a script that ends with a status
# of its own: `part_failed EFFECT REASON || status=1`. It succeeds when Workbench
# reads the report, so the script can end 0 and the scripts after it still run,
# and fails when nobody does (chezmoi run directly), so the script ends nonzero.
part_failed() {
    part_blocked "$@"
    [ -n "${WORKBENCH_EFFECT_REPORT:-}" ]
}
