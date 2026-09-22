# Workbench supplies qualified management owners. Restore their PATH after
# Homebrew/runtime setup, without changing the caller's interactive environment.
if [ -n "${WORKBENCH_TOOL_PATH:-}" ]; then
    export PATH="$WORKBENCH_TOOL_PATH:$PATH"
fi
