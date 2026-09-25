#!/bin/sh
# Install Workbench from its GitHub releases:
#   curl -fsSL https://github.com/Sawmonabo/workbench/releases/latest/download/install.sh | sh
#   gh release download -R Sawmonabo/workbench -p install.sh -O - | sh
# Options: --version vX.Y.Z (default: this release, or the latest). Every other
# argument goes to `workbench update`, for example --config-only or --dry-run.
# The downloaded CLI verifies the whole bundle before it installs anything.
set -eu
repo=Sawmonabo/workbench
default_version=latest # the release workflow stamps each release's tag here

main() {
    version=$default_version
    count=$#
    while [ "$count" -gt 0 ]; do # take --version out, keep the rest in order
        case $1 in
        --version)
            [ "$count" -ge 2 ] || fail '--version needs a value'
            version=$2
            shift 2
            count=$((count - 2))
            continue
            ;;
        --version=*)
            version=${1#*=}
            shift
            count=$((count - 1))
            continue
            ;;
        esac
        set -- "$@" "$1"
        shift
        count=$((count - 1))
    done
    for tool in uname mktemp tar; do
        command -v "$tool" >/dev/null 2>&1 || fail "missing required tool: $tool"
    done
    case $(uname -s) in Darwin) os=darwin ;; Linux) os=linux ;; *) fail 'macOS or Linux required' ;; esac
    case $(uname -m) in arm64 | aarch64) arch=arm64 ;; x86_64 | amd64) arch=amd64 ;; *) fail 'unsupported CPU' ;; esac
    tmp=$(mktemp -d "${TMPDIR:-/tmp}/workbench-install.XXXXXX")
    trap 'rm -rf "$tmp"' EXIT HUP INT TERM
    # gh works while the repository is private; curl needs it public.
    if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
        if [ "$version" = latest ]; then
            version=$(gh release list -R "$repo" -L 1 --exclude-drafts --exclude-pre-releases --json tagName -q '.[0].tagName')
        fi
        check_version
        gh release download "$version" -R "$repo" -p "workbench-$version-$os-$arch.tar.gz" -D "$tmp"
    else
        command -v curl >/dev/null 2>&1 || fail 'missing required tool: curl (or a logged-in gh)'
        if [ "$version" = latest ]; then
            url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")
            version=${url##*/}
        fi
        check_version
        curl -fsSL --proto '=https' -o "$tmp/workbench-$version-$os-$arch.tar.gz" \
            "https://github.com/$repo/releases/download/$version/workbench-$version-$os-$arch.tar.gz"
    fi
    bundle=$tmp/workbench-$version-$os-$arch.tar.gz
    tar -xzf "$bundle" -C "$tmp" bin/workbench
    "$tmp/bin/workbench" update "$version" --bundle "$bundle" "$@"
}

check_version() {
    case $version in v[0-9]*) ;; *) fail "no release found (got '$version')" ;; esac
}

fail() {
    printf 'workbench install: %s\n' "$*" >&2
    exit 1
}

# Defined above, run last: a truncated download cannot run half a script.
main "$@"
