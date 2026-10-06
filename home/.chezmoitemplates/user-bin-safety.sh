# These package-owned executables are external effects, not configuration
# targets. Never follow user-directory aliases or replace an unrelated command.
# A check that refuses says why in $why, which a part shows on its step.
# shown PATH prints PATH, which is under $HOME, as ~/... for those messages.
shown() { printf '%s' "~${1#"$HOME"}"; }
safe_user_directory() {
    local path="$1"
    while [ "$path" != "$HOME" ]; do
        case "$path" in
            "$HOME"/*) ;;
            *)
                why="$path is outside your home folder, and Workbench installs nothing there"
                echo "Unsafe user directory: $path" >&2
                return 1
                ;;
        esac
        if [ -L "$path" ]; then
            why="$(shown "$path") is a link to another folder, and Workbench installs nothing through a link; make it a real folder"
        elif [ -e "$path" ] && [ ! -d "$path" ]; then
            why="$(shown "$path") is a file, not a folder, so Workbench cannot install into it"
        else
            path=${path%/*}
            continue
        fi
        echo "Unsafe user directory: $path" >&2
        return 1
    done
}
require_unused_command() {
    local path="$HOME/.local/bin/$1"
    safe_user_directory "$HOME/.local/bin" || return 1
    if [ -e "$path" ] || [ -L "$path" ]; then
        why="$(shown "$path") already exists and Workbench did not make it, so it was left alone"
        echo "Existing command preserved; resolve ownership before installing: $path" >&2
        return 1
    fi
}
# user_bin_unsafe, unless empty, says why nothing may go into ~/.local/bin.
why=
user_bin_unsafe=
safe_user_directory "$HOME/.local/bin" 2>/dev/null || user_bin_unsafe=$why
[ -n "$user_bin_unsafe" ] || export PATH="$HOME/.local/bin:$PATH"
# user_bin_refused EFFECT... ends the script when ~/.local/bin is unsafe, naming
# the reason on each effect: as a probe line that could not check, or as a
# blocked step (part_failed, from effect-report.sh) for each one ticked.
user_bin_refused() {
    [ -n "$user_bin_unsafe" ] || return 0
    local effect ticked
    for effect in "$@"; do
        if [ "${WORKBENCH_PROBE:-}" = 1 ]; then
            echo "$effect: ? $user_bin_unsafe"
            continue
        fi
        ticked=WORKBENCH_EFFECT_$(tr 'a-z-' 'A-Z_' <<< "$effect")
        [ "${!ticked:-1}" = 1 ] || continue
        part_failed "$effect" "$user_bin_unsafe" || true
    done
    [ "${WORKBENCH_PROBE:-}" = 1 ] && exit 0
    exit 1
}
{{ include ".chezmoitemplates/operation-context.sh" }}
