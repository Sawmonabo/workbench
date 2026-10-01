#!/usr/bin/env bash
# Render the entire chezmoi source tree for one (machine_role, versions_mode)
# pair into a throwaway destination, lint every script, and assert that no
# work-only or machine-specific content leaks. Used by CI on Linux and macOS
# and locally: scripts/render-check.sh personal pinned
#
# A third argument of "wsl" instead fakes a WSL host: the scratch config gets
# is_wsl = true plus the sizing answers that only a real WSL host would prompt
# for, and only the init and script-lint stages run. Apply and the leak checks
# are skipped because they assume the host really is the machine being checked.
set -euo pipefail

usage='usage: render-check.sh <personal|work|both> <pinned|latest> [wsl]'
role=${1:?$usage}
mode=${2:?$usage}
extra=${3:-}
if [ -n "$extra" ] && [ "$extra" != wsl ]; then echo "$usage" >&2; exit 2; fi
repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
# scratch-init.sh puts its config in a directory of its own, so remove the
# directory rather than leaking one mktemp -d per run.
config_dir=
trap 'rm -rf "$tmp" "${config_dir:-}"' EXIT
dest="$tmp/home"
mkdir -p "$dest"
fail=0

echo "==> [$role/$mode] init: every prompt must be answerable non-interactively"
config=$("$repo/scripts/scratch-init.sh" "$role" "$mode")
config_dir=$(dirname "$config")
if [ "$extra" = wsl ]; then
    # The WSL sizing prompts only fire on a real WSL host, so answer them here.
    sed -i.bak 's/^\( *\)is_wsl = false$/\1is_wsl = true/' "$config" && rm -f "$config.bak"
    cat >> "$config" <<'WSLDATA'
    wsl_memory = "8GB"
    wsl_processors = 4
    wsl_swap = "2GB"
    restart_wsl_path = "Desktop/RestartWSL"
WSLDATA
    grep -q 'is_wsl = true' "$config" || { echo "WSL FAIL: could not force is_wsl in $config"; exit 1; }
fi
chez=(chezmoi --config "$config" --source "$repo" --destination "$dest"
    --persistent-state "$tmp/state.boltdb" --cache "$tmp/cache"
    --no-pager --use-builtin-diff --refresh-externals=never --use-builtin-git=true)

if [ "$extra" != wsl ]; then
    echo "==> [$role/$mode] apply into $dest (scripts rendered, never run)"
    "${chez[@]}" diff --exclude scripts >/dev/null
    "${chez[@]}" apply --exclude scripts
fi

echo "==> [$role/$mode] lint scripts"
while IFS= read -r -d '' script; do
    out="$tmp/rendered.sh"
    if ! "${chez[@]}" execute-template < "$script" > "$out"; then
        echo "RENDER FAIL: $script"; fail=1; continue
    fi
    if [ -s "$out" ]; then
        echo "    linting ${script#"$repo"/}"
        bash -n "$out" || { echo "SYNTAX FAIL: $script"; fail=1; }
        shellcheck -S warning "$out" || { echo "SHELLCHECK FAIL: $script"; fail=1; }
        # Probe mode must answer with effect lines only, exit 0, and never
        # reach a writer: every write or prompt sits below the probe block.
        # Only provisioning scripts have a probe mode; the wsl render also
        # lints win-browser, a user binary. The script runs under set -e, so
        # every check here reports instead of aborting the run.
        case "$script" in
        */.chezmoiscripts/*)
            probe_out="$tmp/probe.out"
            if WORKBENCH_PROBE=1 HOME="$dest" bash "$out" >"$probe_out" 2>"$tmp/probe.err" </dev/null; then
                if ! grep -qE '^[a-z0-9-]+: .+$' "$probe_out" || grep -vqE '^[a-z0-9-]+: .+$' "$probe_out"; then
                    echo "PROBE FAIL: $script printed something other than effect lines:"; cat "$probe_out"; fail=1
                fi
            else
                echo "PROBE FAIL: $script exited $? in probe mode:"; cat "$tmp/probe.err"; fail=1
            fi
            probe_end=$(grep -n 'WORKBENCH_PROBE' "$out" | tail -1 | cut -d: -f1 || true)
            if [ -z "$probe_end" ]; then
                echo "PROBE FAIL: $script has no WORKBENCH_PROBE block"; fail=1
            elif head -n "$probe_end" "$out" | grep -vE '^[[:space:]]*#' | grep -qE 'sudo |read -r|curl -f[^ ]* -o|brew install|apt-get install|npm install|cargo install|install -m|cp |mv |> "\$(WSLCONFIG|PS_PROFILE)'; then
                echo "PROBE FAIL: $script has a writer or prompt above its probe block"; fail=1
            fi
            ;;
        esac
        # Comment lines are not behaviour; only executable lines count as a leak.
        if [ "$role" = personal ] && grep -vE '^[[:space:]]*#' "$out" | grep -qiE 'gitlab|bitwarden|fortressinfosec|dispatch-|coderabbit|promptctl'; then
            echo "LEAK: work-only content in rendered script $script (personal)"; fail=1
        fi
    fi
done < <(find "$repo/home/.chezmoiscripts" -name '*.sh.tmpl' -print0 \
    ; [ "$extra" = wsl ] && printf '%s\0' "$repo/home/dot_local/bin/executable_win-browser.tmpl")

if [ "$extra" = wsl ]; then
    if [ "$fail" -ne 0 ]; then echo "FAILED [$role/$mode/wsl-lint]"; exit 1; fi
    echo "OK [$role/$mode/wsl-lint]"
    exit 0
fi

# One shared data-loss safeguard: a malformed existing config must never be
# replaced by a generated body. This reuses the native render fixture, not a
# separate per-script test suite.
echo "==> [$role/$mode] malformed-input preservation"
for relative in .codex/config.toml .claude/settings.json 'Library/Application Support/Code/User/settings.json' .config/Code/User/settings.json; do
    target="$dest/$relative"
    [ -f "$target" ] || continue
    cp "$target" "$tmp/valid-config"
    printf '[invalid input\n' > "$target"
    cp "$target" "$tmp/invalid-config"
    if "${chez[@]}" apply --exclude scripts -- "$target" >"$tmp/invalid-output" 2>&1; then
        echo "PRESERVATION FAIL: invalid $relative was accepted"; fail=1
    fi
    cmp -s "$target" "$tmp/invalid-config" || { echo "PRESERVATION FAIL: invalid $relative was replaced"; fail=1; }
    cp "$tmp/valid-config" "$target"
done
printf '[invalid input\n' > "$tmp/windows-settings.json"
cp "$tmp/windows-settings.json" "$tmp/invalid-config"
if python3 - "$tmp/windows-settings.json" example value < "$repo/home/.chezmoitemplates/merge-json.py" >"$tmp/invalid-output" 2>&1; then
    echo 'PRESERVATION FAIL: invalid Windows settings accepted'; fail=1
fi
cmp -s "$tmp/windows-settings.json" "$tmp/invalid-config" || { echo 'PRESERVATION FAIL: invalid Windows settings replaced'; fail=1; }

echo "==> [$role/$mode] leak checks"
if grep -rIln -e '/home/sabossedgh' -e '/Users/sawmonabo' "$repo/home"; then
    echo "LEAK: hardcoded home directory in a source template"; fail=1
fi
if [ "$role" = personal ]; then
    if grep -rIl -e fortressinfosec -e dispatch-atlassian -e gitlab -e coderabbit -e promptctl "$dest"; then
        echo "LEAK: work-only content rendered for machine_role=personal"; fail=1
    fi
    [ -e "$dest/.codex/hooks.json" ] && { echo "LEAK: .codex/hooks.json deployed on personal"; fail=1; }
    [ -e "$dest/.gitconfig-work" ] && { echo "LEAK: .gitconfig-work deployed on personal"; fail=1; }
fi
if [ "$role" != both ]; then
    [ -e "$dest/.gitconfig-personal" ] && { echo "LEAK: .gitconfig-personal deployed on single-role machine"; fail=1; }
fi
if ! grep -qi microsoft /proc/version 2>/dev/null; then
    [ -e "$dest/.local/bin/win-browser" ] && { echo "LEAK: win-browser deployed on non-WSL host"; fail=1; }
fi
if grep -rIil -e 'cursor' -e 'agent-brain' -e 'ab-claude' "$dest"; then
    echo "LEAK: Cursor or agent-brain content rendered"; fail=1
fi
if grep -rIn -E '"[a-z0-9-]+@[0-9.]+"' "$repo/home/.chezmoidata/packages.toml"; then
    echo "LEAK: versioned package name in packages.toml"; fail=1
fi

echo "==> [$role/$mode] claude code status line"
python3 -m py_compile "$dest/.claude/scripts/statusline.py" || { echo "STATUSLINE FAIL: statusline.py does not compile"; fail=1; }
grep -q 'python3 ~/.claude/scripts/statusline.py' "$dest/.claude/settings.json" || { echo "STATUSLINE FAIL: settings.json does not point at statusline.py"; fail=1; }
grep -q 'statusline-command' "$dest/.claude/settings.json" && { echo "STATUSLINE FAIL: settings.json still references statusline-command.sh"; fail=1; }

if [ "$fail" -ne 0 ]; then echo "FAILED [$role/$mode]"; exit 1; fi
echo "OK [$role/$mode]"
