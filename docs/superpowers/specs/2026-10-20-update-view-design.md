# Update view design

`workbench update` at a terminal draws one live view instead of component
lines and a summary. Every other command keeps its look. This document is the
whole design; [ADR 18](../../adr/0018-update-draws-one-live-view.md) records
why.

## 1. What it looks like

A new release:

```
$ workbench update
[WorkBench] Update · v0.1.27
⠧ Downloading  ━━━━━━━━━━━━━━━━━━━━━━━━  7.9/16.0 MB
```

then, while the release installs and its tools are checked:

```
[WorkBench] Update · v0.1.27
⠧ Installing tools
  chezmoi  2.70.3
  uv       ━━━━━━━━━━━━━━━━━━━━━━━━  4.1/18.4 MB
```

and at the end:

```
[WorkBench] Update · v0.1.27
✓ Updated to v0.1.28
  chezmoi   2.70.3
  uv        0.11.20 → 0.11.21
  Python    3.12.12
  TOML Kit  0.15.1
$
```

The release already installed:

```
[WorkBench] Update · v0.1.28
· Up to date
```

A machine with no release yet:

```
[WorkBench] Install
✓ Installed v0.1.28
  chezmoi   2.70.3
  uv        0.11.21
  Python    3.12.12
  TOML Kit  0.15.1
```

A failure:

```
[WorkBench] Update · v0.1.28
✗ Update failed: uv
  Workbench is installed, but the rest of the update did not finish: HTTPS
  download failed: …
  chezmoi  2.70.3
  uv       failed
```

## 2. Rules

1. **Header.** At the left edge: `[WorkBench]` in the brand color (the apply
   plan header's), bold, then `Update` in bold, then ` · ` and the release
   that was active when the command started, faint. A machine with no release
   yet shows `Install` and no release. With `--local-build` it shows
   `Update · this checkout`. The header never changes during the run, and no
   empty line surrounds it.
2. **One working line** below it, at the left edge, rewritten in place. Its
   first column is a mark: one spinner in the brand color while the update
   works, then the outcome's mark. After the mark it reads `Checking for a
   newer release`, `Downloading` with a bar, `Installing tools`, then the
   outcome.
3. **Bars.** A bar is a line of `━` cells, 24 in a window wide enough, the
   part done in the brand color and the rest faint, followed by
   `done/total MB` in decimal megabytes with one decimal (whole `kB` for a
   file under a megabyte), faint. In a narrower window the bar shortens, down
   to 4 cells, so the size stays in view. A download whose server sends no
   size shows only the bytes so far. Before the first byte the bar is all
   faint, with no size. Where the locale is not UTF-8 the bar is
   `[` + `=` cells + `>` + spaces + `]`.
4. **Tool rows** appear under the working line as the update reaches each
   tool, in the order chezmoi, uv, Python, TOML Kit, indented two spaces,
   names padded to the longest shown name plus two. A tool in place shows its
   version, faint. A tool being downloaded shows its own bar. Python, which
   uv installs with its own progress off, shows a faint `installing`. A tool
   this update fetched shows its version in the terminal's own text color,
   or `PREVIOUS → NEW`, the previous version faint, when it replaced a
   previous private copy of the same tool (going back shows the older version
   on the right). TOML Kit is not a recorded tool, so it shows only its
   version. No headings, and no mark or spinner on a row.
5. **Outcome.** Only after the release and every tool are ready, the working
   line becomes `✓ Updated to vX` in the terminal's own green, or
   `✓ Installed vX` on a machine that had no release. When that release was
   already installed it becomes `· Up to date`, the dot faint, and the rows
   of tools the update did not have to fetch are removed, so a run with
   nothing to do is two lines. With `--local-build` it becomes
   `✓ Installed this checkout's tools` or
   `· Up to date: this checkout's tools`.
6. **Failure.** The working line becomes `✗ Update failed: NAME` in the
   terminal's own red, naming the row that was working or the tool that
   failed the final check of the tools in place, or `✗ Update failed` when
   none did. The reason follows directly under it, indented two spaces and
   wrapped to the window, never cut, and it is not printed a second time as
   an error line. The rows follow, the failed one showing `failed` in red. An
   input error (a malformed VERSION, a flag `--local-build` does not take, an
   empty `--bundle` or a `--bundle` file that does not exist) is not a failed
   update: it prints before any header, as every command prints an error.
7. **Interruption.** An interrupted run shows `· Update stopped`, faint, its
   reason under it like a failure's, and the row that was working `stopped`.
   The exit code is the interruption's.
8. **After the handoff.** If the activated release cannot start after the
   working line was cleared, the failure line and its reason are written
   where the working line was, below any warning about an old release that
   could not be removed.
9. **Up to date.** The working line stays `Checking for a newer release`
   while the tools are checked and becomes `Installing tools` only when a
   tool has to be fetched.
10. **Marks.** `✓`, `·` and `✗` are the marks every command's result lines
    use. Where the locale is not UTF-8 they are the plain marks those lines
    use there, the spinner is a turning line and `→` is `->`.
11. No sentence about applying: the outcome is the last line.

## 3. Where it is drawn

- At a terminal, in an interactive run (no `--json`, no `--non-interactive`):
  both stdout, where the result lines go, and stderr, where progress goes,
  are the terminal, and it reports a width and a height above 0. Elsewhere,
  including `update > log`, `update | tee log` or a pseudo-terminal opened
  with no size (`ssh -tt` from a script), `update` prints the status line or
  plain progress lines, the component lines and the summary. `NO_COLOR`
  keeps the view's layout without color, as every command does.
- `--dry-run` prints the install step, with no header.
- The view reads no input and sends the terminal no query, as the progress
  line does, so nothing can reach the next prompt.
- Lines other than a reason are clipped to the terminal width so a redraw
  never wraps.
- Only the working part is redrawn in place. When the update ends, that part
  is cleared and the outcome, its reason and the rows are printed as plain
  lines: a redrawn frame taller than the window loses its first lines, which
  in a short window would be the outcome and the start of a long reason.
- `update --list` updates nothing and keeps the status line.

## 4. Two processes, one screen

The running release checks, downloads and activates the new one, then hands
the terminal to it (`syscall.Exec`), and the new one installs the tools. The
first process prints the header and draws the working line; once the
handoff's checks pass, just before the exec, it clears the working line,
leaves the cursor on it and prints any warning from removing old releases.
The second process starts its view there, before its own checks, so a
failure in them still shows `Update failed`.

The handoff's one argument, `--runtime-ready`, says whether the release was
`installed`, was `unchanged`, or is the `first` on the machine, which is how
the second process knows to say `Installed`. No flag is added: an older
release can be the one activated (`update VERSION` goes back) and would
refuse a flag it does not know, while it reads any value other than
`unchanged` as an install. A release without the view hands over with its
own last line and draws no header; the view then starts below it, once. A
release without the view that is activated prints its own lines below the
header.

## 5. Ownership

- `internal/operation` carries presentation-free events: a tool's state,
  version and previous version, and a download's bytes and size. A progress
  that does not handle them ignores them, so `apply`, which installs the same
  tools, keeps its status line.
- `internal/download` counts the bytes of each HTTPS download.
- `internal/machine` reports each tool as it reaches it.
- `internal/cli` owns the view, its words and its colors.
