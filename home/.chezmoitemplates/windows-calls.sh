# Calls from WSL to Windows, started together and waited for together.
#
# Windows answers slowly when it has been idle, and a probe has 15 s in all, so
# the calls that do not depend on each other run side by side under one cap
# instead of one after another. A probe uses a short cap and reports a call that
# misses it as one it could not check; an apply is not on a clock and waits
# longer. Output goes to files, never a pipe: a Windows process that outlives
# its stopped call must not be able to hold the wait open.
#
#   win_start NAME COMMAND...   run COMMAND in the background
#   win_wait                    wait for every call, then read WIN_OUT[NAME]
#                               (its output) and WIN_RC[NAME] (its exit status)
#   win_unanswered NAME         say why NAME gave no answer and succeed, or fail
#                               when it answered
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then win_cap=8; else win_cap=60; fi
declare -A WIN_OUT=() WIN_RC=()
win_work=$(mktemp -d)

win_start() {
    local name=$1
    shift
    (
        rc=0
        timeout -k 1 "$win_cap" "$@" >"$win_work/$name.out" 2>/dev/null </dev/null || rc=$?
        echo "$rc" >"$win_work/$name.rc"
    ) &
}

win_wait() {
    local file name
    wait
    for file in "$win_work"/*.rc; do
        [ -e "$file" ] || continue
        name=$(basename "$file" .rc)
        WIN_RC[$name]=$(<"$file")
        WIN_OUT[$name]=$(tr -d '\0\r' <"$win_work/$name.out")
    done
    rm -rf "$win_work"
}

win_unanswered() {
    case "${WIN_RC[$1]:-}" in
    0) return 1 ;;
    124 | 137) echo "Windows didn't answer in time" ;;
    *) echo "Couldn't check the Windows side" ;;
    esac
}
