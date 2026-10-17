# 11. Releases reach a machine through full, pre-release and local channels

- Status: Accepted, October 17, 2026

## Context

`workbench update` without a version installs GitHub's latest release and
never goes back from a newer one. Code that is not yet a full release still
has to be tried on a real machine before it ships. A bundle built on a
developer's computer can be installed with `--bundle`, but nothing ties it to
a commit anyone else can check out, and its name, not its origin, decides how
it sorts against published releases.

GitHub's latest release is "the most recent non-prerelease, non-draft
release", and `gh release create --prerelease` marks a release as a
pre-release. Semantic Versioning gives a pre-release (`1.0.0-rc.1`) lower
precedence than its normal version (`1.0.0-rc.1 < 1.0.0`).

## Options considered

- **A. Three channels: full releases (`vX.Y.Z`), pre-releases
  (`vX.Y.Z-rc.N`, published by the same workflow and marked as GitHub
  pre-releases) and local builds, which only disposable homes and CI run.**
  Chosen.
- **B. Install unreleased code on a real machine as a local build.** Nothing
  ties the build to a commit anyone can check out, and the next `update`
  either keeps it or replaces it depending on its name.
- **C. Publish every candidate as a full release.** A candidate becomes
  GitHub's latest release, so every machine's `update` installs it, and the
  changelog gains a section per candidate.

## Decision

- **Full release.** Pushing a `vX.Y.Z` tag builds the bundles with
  `scripts/package-release.py --published`, publishes them as a normal GitHub
  release, and commits the regenerated `CHANGELOG.md`. It becomes GitHub's
  latest release, which `update` and `install.sh` follow.
- **Pre-release.** Pushing a `vX.Y.Z-rc.N` tag (any tag with a hyphen) builds
  the same `--published` bundles and publishes them with `gh release create
  --prerelease`. GitHub's latest release stays the newest full release, so no
  machine moves to the pre-release unless it asks: `workbench update
  vX.Y.Z-rc.N`, or `install.sh --version vX.Y.Z-rc.N`. A pre-release is on
  the release channel, so `update` without a version keeps it while it is
  newer than the latest full release and installs `vX.Y.Z` once that is
  published, because semantic versioning orders `vX.Y.Z-rc.N` below `vX.Y.Z`.
  `workbench version --list` lists full releases only; `gh release list -R
  Sawmonabo/workbench` and the repository's releases page also show
  pre-releases.
- **Changelog.** `cliff.toml` matches only full release tags, so a
  pre-release gets no section and its commits belong to the full release that
  follows it. The changelog job does not run for a pre-release, and its check
  for the newest tag leaves pre-release tags out, because git's `v:refname`
  order puts `v0.2.0-rc.1` after `v0.2.0`. A pre-release's notes are written
  with the tag pattern widened to every `v*` tag: the commits since the tag
  before it. git-cliff's `ignore_tags` was tried first, which its
  documentation says moves an ignored tag's commits into the next tag; it
  does for the whole changelog, but `--latest` then printed an empty
  `Unreleased` section for the full release that followed a pre-release.
- **Local build.** A bundle built without `--published` is local. Its name is
  the next release with a `-dev.N` suffix, so it sorts below that release.
  `update` without a version, run by a local build that is the active
  release, replaces it with the latest published release whatever the
  versions, and `workbench version` prints `(local build)` after its version.
  Local builds run only in disposable homes and CI; unreleased code reaches a
  real machine only as a pre-release.

## Consequences

- Trying unreleased code on a real machine needs a pushed pre-release tag,
  which only the owner pushes, and the tag names the exact commit installed.
- A machine on a pre-release stays on it until the matching full release is
  published, or until it names another version.
- Pre-releases published before a full release add commits to that full
  release's changelog section, not sections of their own.

## Sources

- [GitHub REST API: get the latest release](https://docs.github.com/en/rest/releases/releases#get-the-latest-release)
- [GitHub REST API: get a release by tag name](https://docs.github.com/en/rest/releases/releases#get-a-release-by-tag-name)
- [GitHub CLI: `gh release create --prerelease`](https://cli.github.com/manual/gh_release_create)
- [Semantic Versioning 2.0.0, pre-release (item 9)](https://semver.org/#spec-item-9)
- [Semantic Versioning 2.0.0, precedence (item 11)](https://semver.org/#spec-item-11)
- [git-cliff configuration: `tag_pattern`, `ignore_tags`](https://git-cliff.org/docs/configuration/git)
- [git-cliff arguments: `--latest`, `--tag-pattern`](https://git-cliff.org/docs/usage/args)
- [Deno: build-time release channel, `deno upgrade`](https://docs.deno.com/runtime/reference/cli/upgrade/)
