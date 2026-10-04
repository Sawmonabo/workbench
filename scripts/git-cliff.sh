#!/usr/bin/env bash
# Run the pinned git-cliff release with the given arguments, after checking its
# checksum. Used by the release workflow on its x86_64 Linux runners.
set -euo pipefail
version=2.14.2
# SHA-256 of the official v2.14.2 x86_64-unknown-linux-gnu asset; it matches
# the digest in upstream's .sha512 file for the same asset.
sha256=24f397c733add5390fdceee3a2088588ab0d5f944ce00d34cb7029b888cf2db4
dir=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/git-cliff.XXXXXX")
trap 'rm -rf "$dir"' EXIT
curl -fsSL --retry 3 -o "$dir/git-cliff.tar.gz" \
    "https://github.com/orhun/git-cliff/releases/download/v$version/git-cliff-$version-x86_64-unknown-linux-gnu.tar.gz"
printf '%s  %s\n' "$sha256" "$dir/git-cliff.tar.gz" | sha256sum --check --strict
tar -xzf "$dir/git-cliff.tar.gz" -C "$dir" "git-cliff-$version/git-cliff"
"$dir/git-cliff-$version/git-cliff" "$@"
