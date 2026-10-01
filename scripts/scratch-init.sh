#!/usr/bin/env bash
# Generate a throwaway chezmoi config for one (machine_role, versions_mode)
# pair with every prompt answered non-interactively, and print its path.
# Never touches ~/.config/chezmoi.
#
# chezmoi keys --prompt* flags on the prompt TEXT shown to the user, not on the
# data field name, so the strings below must match home/.chezmoi.toml.tmpl
# exactly. The WSL sizing prompts embed host numbers in their text, so they
# are answered by seeding the config instead: the *Once prompt functions reuse
# a value the existing config already holds, which also skips the Windows host
# probe. The seed only matters on a real WSL host; elsewhere init drops it.
set -euo pipefail

role=${1:?usage: scratch-init.sh <personal|work|both> <pinned|latest>}
mode=${2:?usage: scratch-init.sh <personal|work|both> <pinned|latest>}
repo=$(cd "$(dirname "$0")/.." && pwd)
scratch=$(mktemp -d)
config="$scratch/chezmoi.toml"
trap 'rm -rf "$scratch"' EXIT
mkdir -p "$scratch/source" "$scratch/destination" "$scratch/cache"
cat > "$config" <<'SEED'
[data]
    wsl_memory = "8GB"
    wsl_processors = 4
    wsl_swap = "2GB"
    restart_wsl_path = "Desktop/RestartWSL"
SEED
cp "$repo/.chezmoiroot" "$scratch/source/"
cp -R "$repo/home" "$scratch/source/home"

chezmoi --config "$config" --source "$scratch/source" --destination "$scratch/destination" \
    --persistent-state "$scratch/state.boltdb" --cache "$scratch/cache" \
    --no-pager --use-builtin-diff --refresh-externals=never --use-builtin-git=true init \
    --promptChoice "Machine role=$role" \
    --promptChoice "Preferred editor=code" \
    --promptChoice "Install pinned or latest tool versions=$mode" \
    --promptString "Git user name=CI User" \
    --promptString "Git default email (every repo without an override)=ci@example.invalid" \
    --promptString "Git personal email (repos under ~/dev/)=ci-personal@example.invalid" \
    --promptString "Git work email (repos under ~/repos/)=ci-work@example.invalid" \
    --promptString "Jira API token (for Codex MCP; stored locally only)=ci-placeholder" \
    --promptString "GitLab token (for Codex MCP; stored locally only)=ci-placeholder" \
    >/dev/null

trap - EXIT
echo "$config"
