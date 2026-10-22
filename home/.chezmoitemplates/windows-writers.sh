# Which WSL distribution writes each Windows file only one of them may write.
#
# Every distribution on the PC shares one Windows user, so the record lives on
# Windows, in <LocalAppData>\Workbench\writers.json, keyed by step:
# {"terminal-adoption": "AlmaLinux-10", ...}. It holds only distribution names.
# Deleting it is safe: nobody is recorded then. The rules, the same in every
# distribution:
#   1. nobody is recorded, or the recorded distribution is no longer installed:
#      a distribution with the step ticked writes, and records itself
#   2. this distribution is recorded: it writes
#   3. another installed distribution is recorded: the step changes nothing,
#      whatever this distribution's saved choice; only its take-over-<step>
#      effect, ticked in workbench apply --choose, moves it here
#
#   writer_read STEP RECORD_FILE
#       read the distribution recorded for STEP into WRITER, "" when nobody
#       is; it fails when the record cannot be read, and WIN_RC[writer-read]
#       then says whether Windows answered in time
#   writer_line STEP RECORD_FILE INSTALLED_LIST THIS_DISTRO
#       the probe line rule 3 gives ("STEP: = written by NAME"), which stands
#       for the step's own line; a "?" line when the rule cannot be told (the
#       record cannot be read, or another distribution is recorded and this
#       distribution's name, or an installed list naming it, is missing);
#       nothing under rules 1 and 2, where the step's own line stands alone.
#       INSTALLED_LIST is wsl.exe --list --quiet's answer, one name a line,
#       and empty when that call did not succeed.
#   writer_record STEP RECORD_FILE THIS_DISTRO
#       record THIS_DISTRO as STEP's writer, replacing the whole file at once
#   writer_runs STEP SELECTED RECORD_FILE INSTALLED_LIST THIS_DISTRO
#       whether an apply runs STEP: its take-over is ticked, or SELECTED is 1
#       and rule 3 does not hold. A rule it cannot tell blocks the step
#       (part_blocked, from effect-report.sh).
#
# The record is read and written through win_call (windows-calls.sh, included
# first), so a Windows drive that hangs costs the cap every Windows call has:
# 60 s in an apply, the probe's shorter one in a probe. A read that did not
# finish is a rule that cannot be told, never leave to write, and a write that
# did not finish fails.
#
# Plain bash, with no template directive, so a check can source it.

writer_read() {
    WRITER=
    [ -n "$2" ] || return 1
    win_call writer-read python3 -I -c '
import json, sys
path, step = sys.argv[1:]
try:
    with open(path, encoding="utf-8") as stream:
        record = json.load(stream)
except FileNotFoundError:
    record = {}
writer = record.get(step, "") if isinstance(record, dict) else None
if not isinstance(writer, str):
    sys.exit(1)
print(writer)
' "$2" "$1"
    [ "${WIN_RC[writer-read]}" = 0 ] || return 1
    WRITER=${WIN_OUT[writer-read]}
}

writer_line() {
    local step=$1 record=$2 installed=$3 this=$4 writer
    # An empty path is no record that could be missing: Windows did not say
    # where its local app data is.
    if [ -z "$record" ]; then
        echo "$step: ? Couldn't read the Windows writer record"
        return
    fi
    if ! writer_read "$step" "$record"; then
        case ${WIN_RC[writer-read]} in
        124 | 137) echo "$step: ? Windows didn't answer in time when the writer record was read" ;;
        *) echo "$step: ? Couldn't read the Windows writer record" ;;
        esac
        return
    fi
    writer=$WRITER
    if [ -z "$writer" ] || [ "$writer" = "$this" ]; then
        return
    fi
    if [ -z "$this" ]; then
        echo "$step: ? WSL_DISTRO_NAME is not set, so whether $writer or this distribution writes it is unknown"
        return
    fi
    # A listing that did not answer, or that does not even name the
    # distribution running this, is no proof the writer is gone.
    if [ -z "$installed" ] || ! grep -qxF -- "$this" <<<"$installed"; then
        echo "$step: ? Couldn't check whether $writer, which writes it, is still installed"
    elif grep -qxF -- "$writer" <<<"$installed"; then
        echo "$step: = written by $writer"
    fi
}

writer_record() {
    [ -n "$2" ] && [ -n "$3" ] || return 1
    win_call writer-write python3 -I -c '
import json, os, sys, tempfile
path, step, distro = sys.argv[1:]
try:
    with open(path, encoding="utf-8") as stream:
        record = json.load(stream)
except FileNotFoundError:
    record = {}
if not isinstance(record, dict):
    sys.exit("the record is not a JSON object")
record[step] = distro
folder = os.path.dirname(path)
os.makedirs(folder, exist_ok=True)
descriptor, staged = tempfile.mkstemp(dir=folder, prefix=".writers.", suffix=".part")
try:
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        json.dump(record, stream, indent=2, sort_keys=True)
        stream.write("\n")
    os.replace(staged, path)
except BaseException:
    os.unlink(staged)
    raise
' "$2" "$1" "$3"
    [ "${WIN_RC[writer-write]}" = 0 ]
}

writer_runs() {
    local step=$1 selected=$2 line take_over
    take_over=${step^^}
    take_over=WORKBENCH_EFFECT_TAKE_OVER_${take_over//-/_}
    [ "${!take_over:-0}" != 1 ] || return 0
    [ "$selected" = 1 ] || return 1
    line=$(writer_line "$step" "$3" "$4" "$5")
    case $line in
    "$step: = "*)
        echo "==> $step skipped: ${line#"$step: = "} (workbench apply --choose offers to take it over)."
        return 1
        ;;
    "$step: ? "*)
        part_blocked "$step" "${line#"$step: ? "}, so it was not changed"
        return 1
        ;;
    esac
}
