# 14. The guard memory view leaves the mouse to the terminal

- Status: Accepted, October 18, 2026

## Context

`workbench guard memory` is a full-screen view on the terminal's alternate
screen (ADR 13). Two things a person reaches for there, the mouse wheel to
scroll and a drag to select and copy text, pull against each other in a
terminal:

- A program can report the mouse (xterm's mouse tracking modes). The wheel
  then reaches the program, which scrolls what it likes, and a click can
  select a row. But while the mouse is reported the terminal no longer
  selects text under a plain drag; a menu toggle or a held key is needed,
  and which one varies by terminal (Terminal.app: View → Allow Mouse
  Reporting, ⌘R).
- A program can leave the mouse alone. A plain drag selects text, and most
  terminals send a wheel step on the alternate screen as ↑ or ↓, so a view
  that moves on those keys scrolls under the wheel too. In xterm that is
  alternate scroll mode (DECSET 1007, off by default there); Terminal.app
  has a profile setting for it and knows nothing of the mode.

The view first did both: it reported the mouse on the list, for a click to
select a row, and released it in the details, for a plain drag. In
Terminal.app the details then scrolled the terminal's own window instead.
Of the full-screen tools in daily use on the same machine, the ones that
never report the mouse scroll correctly there, and so do the ones that
report it on every screen; this view was the only one that reported the
mouse on one screen and released it on another, and the only one that
failed, so that switch is what the decision removes. Setting alternate
scroll mode made no difference, since Terminal.app ignores it, and the mode
query sent before it (DECRQM) printed a stray `p`, since Terminal.app does
not parse that either.

## Decision

The view never reports the mouse, on any screen. It sets xterm's alternate
scroll mode as it starts, saving the terminal's state first (XTSAVE), and
restores that state (XTRESTORE) when it ends, for terminals that need the
mode; it sends no mode query. The exit is a restore alone, not a reset: a
terminal without XTSAVE, Windows Terminal among them, ignores the restore
and keeps the mode as its own setting left it, where a reset would have
turned a default-on setting off for the rest of the tab. Clicking a row no
longer does anything: ↑ and ↓ move, Enter, → or `l` opens the details, as
before.

The costs view is unchanged: it reports the mouse on every screen and never
switches, which Terminal.app handles.

## Consequences

- The wheel moves the list, scrolls the details and moves the stop menu's
  choice, as the arrow keys do: in xterm under the mode; in Windows
  Terminal, Ghostty and VTE terminals, which have the mode and, except
  xterm, have it on by default (sources below); and in Terminal.app through
  its own profile setting.
- A plain drag selects text on every screen, with no key held and no
  terminal setting changed.
- A click on a row does nothing; the view is keyboard-driven, as `less` and most
  full-screen terminal tools are.
- A wheel step over the stop menu moves its choice between `Stop` and
  `Force kill now`; Enter is still needed, so a flick stops nothing by
  itself.
- xterm, where alternate scroll is off by default, has it on while the view
  runs and off again after it; a terminal without XTSAVE keeps its own
  setting; a terminal without the mode and without a setting of its own
  scrolls its window under the wheel, as it does for `less`.

## Sources

- [xterm Control Sequences, "Wheel mice" and DECSET 1007](https://invisible-island.net/xterm/ctlseqs/ctlseqs.html):
  the wheel scrolls the window by default; with alternate scroll set, cursor
  up and down are sent on the alternate screen.
- [Apple, Turn on Mouse Reporting in Terminal on Mac](https://support.apple.com/guide/terminal/turn-on-mouse-reporting-trmlc69728a5/mac):
  mouse reporting is allowed by default (View → Allow Mouse Reporting); while
  it is, a program that enables reporting gets the mouse events.
- [Apple, Keyboard shortcuts in Terminal on Mac](https://support.apple.com/guide/terminal/keyboard-shortcuts-trmlshtcts/mac):
  ⌘R toggles Allow Mouse Reporting.
- [vtdn, Alt Scroll (mode 1007)](https://vtdn.dev/docs/decset/mode1007-alt-scroll/):
  the mode's definition and default.
- [Windows Terminal issue 13187](https://github.com/microsoft/terminal/issues/13187):
  every terminal tested that has alternate scroll has it on by default,
  except xterm; Windows Terminal turned it on by default in PR 16535.
- [Windows Terminal issue 10153](https://github.com/microsoft/terminal/issues/10153):
  XTSAVE and XTRESTORE are not implemented there.
