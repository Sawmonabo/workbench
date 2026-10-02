# Calls from WSL to Windows, capped, and cleaned up behind.
#
# Windows answers slowly when it has been idle, and a probe has 15 s in all, so
# the calls that do not depend on each other run side by side under one cap
# instead of one after another. A probe uses a short cap and reports a call that
# misses it as one it could not check; an apply waits longer, but never without
# end: every call Windows is asked for goes through here, so a Windows that does
# not answer costs the cap and one part, not the apply. Output goes to files,
# never a pipe: a Windows process that outlives its stopped call must not be able
# to hold the wait open.
#
# Each call is run by `timeout`, which leads its own process group, so a Ctrl-C
# or a kill of the script does not reach it. The exit trap here stops the group
# of every call still running and removes the output folder, on exit, interrupt,
# hang-up, termination or error. A script that sets its own EXIT trap must run
# win_cleanup from it.
#
#   win_start NAME COMMAND...   run COMMAND in the background
#   win_wait                    wait for the calls started, then read WIN_OUT[NAME]
#                               (its output) and WIN_RC[NAME] (its exit status)
#   win_call NAME COMMAND...    win_start and win_wait for one call
#   win_unanswered NAME         say why NAME gave no answer and succeed, or fail
#                               when it answered
#   win_failed NAME             the same for an apply: why a call that did not
#                               succeed did not, in plain words
#   part_blocked EFFECT REASON  say that one effect could not be done, without
#                               stopping the rest of the script: it is named on
#                               stderr and in the file Workbench reads
#                               ($WORKBENCH_EFFECT_REPORT) to show it on that
#                               effect's result line
if [ "${WORKBENCH_PROBE:-}" = 1 ]; then win_cap=8; else win_cap=60; fi
declare -A WIN_OUT=() WIN_RC=() win_pid=()
win_work=$(mktemp -d)

win_cleanup() {
    local name pid
    for name in "${!win_pid[@]}"; do
        pid=${win_pid[$name]}
        # The call leads its own group: stop it and anything it started.
        kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
        kill -KILL -- "-$pid" 2>/dev/null || true
    done
    win_pid=()
    rm -rf "$win_work"
}
trap win_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

win_start() {
    local name=$1
    shift
    timeout -k 1 "$win_cap" "$@" >"$win_work/$name.out" 2>/dev/null </dev/null &
    win_pid[$name]=$!
}

win_wait() {
    local name rc
    for name in "${!win_pid[@]}"; do
        rc=0
        wait "${win_pid[$name]}" || rc=$?
        WIN_RC[$name]=$rc
        WIN_OUT[$name]=$(tr -d '\0\r' <"$win_work/$name.out")
    done
    win_pid=()
}

win_call() {
    win_start "$@"
    win_wait
}

win_unanswered() {
    case "${WIN_RC[$1]:-}" in
    0) return 1 ;;
    124 | 137) echo "Windows didn't answer in time" ;;
    *) echo "Couldn't check the Windows side" ;;
    esac
}

win_failed() {
    case "${WIN_RC[$1]:-}" in
    124 | 137) echo "Windows didn't answer within $win_cap seconds" ;;
    *) echo "Windows reported an error" ;;
    esac
}

part_blocked() {
    echo "WORKBENCH_EFFECT $1 blocked: $2" >&2
    if [ -n "${WORKBENCH_EFFECT_REPORT:-}" ]; then
        printf '%s\t%s\n' "$1" "$2" >>"$WORKBENCH_EFFECT_REPORT"
    fi
}
