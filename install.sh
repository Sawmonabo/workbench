#!/bin/sh
# Minimal authenticated bootstrap. All archive/setup/lifecycle work is in Go.
set -eu
umask 077
fail() { printf '%s\n' "$*" >&2; exit 1; }
for utility in uname mktemp mkdir chmod curl rm rmdir; do
    command -v "$utility" >/dev/null 2>&1 || fail "Missing bootstrap prerequisite: $utility"
done
if command -v sha256sum >/dev/null 2>&1; then
    hash() { sha256sum "$1"; }
elif command -v shasum >/dev/null 2>&1; then
    hash() { shasum -a 256 "$1"; }
else
    fail 'Missing bootstrap prerequisite: sha256sum or shasum'
fi
case $(uname -s) in Darwin) os=darwin;; Linux) os=linux;; *) fail 'Only macOS and Linux/WSL bundles are available';; esac
case $(uname -m) in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=amd64;; *) fail 'Unsupported CPU architecture';; esac
case " $* " in *' --dry-run '*)
    printf 'Bootstrap only: would verify a %s-%s CLI then hand off to its release installer. No configuration preview is available before qualified management tools and answers exist.\n' "$os" "$arch"
    exit 0;;
esac
: "${WORKBENCH_BOOTSTRAP_SHA256:?Set an explicitly trusted standalone CLI SHA-256}"
case "$WORKBENCH_BOOTSTRAP_SHA256" in *[!0-9a-f]*|'') fail 'Invalid SHA-256';; esac
[ ${#WORKBENCH_BOOTSTRAP_SHA256} -eq 64 ] || fail 'SHA-256 must contain 64 lowercase hexadecimal digits'
scratch=$(mktemp -d "${TMPDIR:-/tmp}/workbench-bootstrap.XXXXXXXX")
trap 'rm -f "$scratch/workbench" "$scratch/auth"; rmdir "$scratch"' EXIT HUP INT TERM
if [ -n "${WORKBENCH_BOOTSTRAP_FILE:-}" ]; then
    command -v cp >/dev/null 2>&1 || fail 'Local bootstrap input requires cp'
    [ -f "$WORKBENCH_BOOTSTRAP_FILE" ] || fail 'Local bootstrap input must be a regular executable file'
    cp "$WORKBENCH_BOOTSTRAP_FILE" "$scratch/workbench"
else
    : "${WORKBENCH_BOOTSTRAP_URL:?Set the HTTPS standalone CLI asset URL}"
    case "$WORKBENCH_BOOTSTRAP_URL" in https://*) ;; *) fail 'Bootstrap URL must use HTTPS';; esac
if [ -n "${WORKBENCH_GITHUB_TOKEN:-}" ]; then
    case "$WORKBENCH_GITHUB_TOKEN" in *[![:graph:]]*) fail 'Invalid credential characters';; esac
    case "$WORKBENCH_BOOTSTRAP_URL" in https://api.github.com/repos/Sawmonabo/workbench/releases/assets/[0-9]*) ;; *) fail 'Credentials require the private Workbench release-asset API URL';; esac
    # curl reads a private header file so the token never appears in argv.
    printf 'Authorization: Bearer %s\nAccept: application/octet-stream\n' "$WORKBENCH_GITHUB_TOKEN" > "$scratch/auth"
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-redirs 5 --connect-timeout 15 --max-time 120 --max-filesize 134217728 --header "@$scratch/auth" "$WORKBENCH_BOOTSTRAP_URL" --output "$scratch/workbench"
else
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-redirs 5 --connect-timeout 15 --max-time 120 --max-filesize 134217728 "$WORKBENCH_BOOTSTRAP_URL" --output "$scratch/workbench"
fi
fi
actual=$(hash "$scratch/workbench")
actual=${actual%% *}
[ "$actual" = "$WORKBENCH_BOOTSTRAP_SHA256" ] || fail 'Bootstrap checksum mismatch; executable was not run'
chmod 700 "$scratch/workbench"
"$scratch/workbench" install "$@"
