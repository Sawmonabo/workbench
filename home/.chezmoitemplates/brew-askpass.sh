# During an apply at a terminal, Workbench answers Homebrew's sudo requests with
# the Mac password it asked for once (WORKBENCH_SUDO_ASKPASS is the helper that
# asks it). Only brew gets SUDO_ASKPASS, never the rest of the script; without
# the helper, brew asks for itself.
if [ -n "${WORKBENCH_SUDO_ASKPASS:-}" ] && command -v brew >/dev/null 2>&1; then
    brew() { SUDO_ASKPASS="$WORKBENCH_SUDO_ASKPASS" command brew "$@"; }
fi
