# These package-owned executables are external effects, not configuration
# targets. Never follow user-directory aliases or replace an unrelated command.
safe_user_directory() {
    local path="$1"
    while [ "$path" != "$HOME" ]; do
        case "$path" in "$HOME"/*) ;; *) echo "Unsafe user directory: $path" >&2; return 1 ;; esac
        [ ! -L "$path" ] && { [ ! -e "$path" ] || [ -d "$path" ]; } || { echo "Unsafe user directory: $path" >&2; return 1; }
        path=${path%/*}
    done
}
require_unused_command() {
    local path="$HOME/.local/bin/$1"
    safe_user_directory "$HOME/.local/bin"
    [ ! -e "$path" ] && [ ! -L "$path" ] || { echo "Existing command preserved; resolve ownership before installing: $path" >&2; return 1; }
}
safe_user_directory "$HOME/.local/bin"
export PATH="$HOME/.local/bin:$PATH"
{{ include ".chezmoitemplates/operation-context.sh" }}
