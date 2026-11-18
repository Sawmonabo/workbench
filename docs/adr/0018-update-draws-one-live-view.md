# 18. Update draws one live view at a terminal

- Status: Accepted, November 18, 2026

## Context

`workbench update` printed what every command prints: a line before the
handoff to the activated release, one `name: status — message` line per part
and a summary line. A run with nothing new said "already installed" three
times, its first line had no color while its last did, and none of it showed
progress beyond the shared status line's title.

Installers that read well show work as it happens and keep only what
changed or what a person needs after the run: uv, bun, pnpm, cargo and npm
redraw a live line while working and leave a short record, and none repeats
the outcome. An update also runs as two processes: the release that starts it
checks, downloads and activates the new one, then hands the terminal to it
with `syscall.Exec`, and the new one installs the tools.

## Options considered

- **A. One live view: a fixed header, one working line rewritten in place,
  a row per tool, the outcome on the working line.** Chosen. The header and
  the working line sit at the left edge and the rows are indented under
  them, the header names the command as the apply plan's does, and the
  working line's first column holds the spinner and then the mark every
  command's result lines use, so the view reads as part of the same CLI.
- **B. A line per step with a check mark, each step kept.** Every run, even
  one with nothing to do, prints the same block of marks, and the outcome is
  one more line to find.
- **C. Keep the component lines and restyle them.** The repetition and the
  missing progress stay.

## Decision

At a terminal that reports its size, in an interactive run with both stdout
and stderr on it, `update` draws the view described in the
[update view design](../superpowers/specs/2026-10-20-update-view-design.md).
Elsewhere, with either output piped or redirected, at a terminal with no
size, with `--json` or with `--non-interactive`, it prints what it printed
before, with one change: a warning that an old release could not be removed
now prints just before the handoff, or, when the update fails before the
handoff, with the result's other warnings, which `--json` carries in
`warnings` instead of as text on stderr.

The starting release prints the header and draws the working line; once the
handoff's checks pass, just before the exec, it clears that line and leaves
the cursor on it, and the activated release draws its own view from there.
No flag is added to the handoff, since an older release can be the one
activated and would refuse a flag it does not know; its existing argument
gains the value `first`, for a machine that had no release, which an older
release reads as an install. Download bytes and
tool states reach the view through progress events in `internal/operation`
that other commands' progress ignores, so `apply`, which installs the same
tools, keeps its status line.

The outcome uses the terminal's own green, and a failure its own red: each
terminal theme makes those readable on its background, and choosing between a
light and a dark color would mean asking the terminal for its background,
whose late answer can reach the next prompt.

## Consequences

- An update with nothing new is two lines; an install shows each download's
  bar while it runs and ends with a row for each tool.
- The first update to a release with the view is run by a release without it,
  whose own handoff line comes first; every later update draws the whole view.
  Going back to such a release prints its old lines below the header.
- A local build replaced by the latest published release shows the move in
  the header and the outcome; the line naming the replacement prints only
  without a terminal and in `--dry-run`.
- The reason of a failure or a stop is part of the view, under its outcome
  line, and is not printed again as an error line. The exit code is
  unchanged.
- The outcome is printed as plain lines once the live part has ended, so a
  window shorter than the outcome and a long reason scrolls instead of
  losing lines.
- A terminal that reports no size gets plain progress lines from every
  command, where the status line and the view draw nothing.
