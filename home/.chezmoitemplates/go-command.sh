# go_command prints the go to use: ~/.local/bin/go, where the Runtime managers
# step links the Go it installs, when that is there, else the first go on PATH;
# it fails when there is none. The PATH an apply gives its scripts can put a
# system go first (AlmaLinux's golang package in /usr/bin), which must not hide
# the one Workbench put in place.
go_command() {
    if [ -x "$HOME/.local/bin/go" ]; then
        printf '%s\n' "$HOME/.local/bin/go"
    else
        command -v go 2>/dev/null
    fi
}
