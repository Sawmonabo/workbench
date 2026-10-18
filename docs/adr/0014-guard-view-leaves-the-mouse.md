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
- A program can leave the mouse alone. A plain drag selects text, and
  terminals send a wheel step on the alternate screen as ↑ or ↓ under a
  terminal mode, so a view that moves on those keys scrolls under the wheel
  too. In xterm that is alternate scroll mode (DECSET 1007, off by default
  there). Terminal.app knows nothing of that mode: it sends the wheel as
  arrow keys only while the program has application cursor keys on (DECCKM,
  DECSET 1), which full-screen programs turn on through terminfo's `smkx`
  (together with the application keypad) and which `less` and `vim` send by
  default; that is why they scroll there and a Bubble Tea view, which never
  sets it, does not. A byte probe in Terminal.app 2.15 showed it: on the
  alternate screen the wheel sent nothing, nothing with 1007 set, and
  `ESC O B` / `ESC O A` with DECCKM set; the name of its hidden preference
  `ScrollAlternateScreenWithoutApplicationCursorMode` suggests the rule.

The view first did both: it reported the mouse on the list, for a click to
select a row, and released it in the details, for a plain drag. In
Terminal.app the details then scrolled the terminal's own window instead.
The full-screen tools in daily use on the same machine that never report
the mouse, `less` and `vim`, scroll there because they set application
cursor keys; the ones that report the mouse on every screen get the wheel
as mouse events. The view did neither in the details. Setting alternate
scroll mode made no difference, since Terminal.app ignores it, and the mode
query sent before it (DECRQM) printed a stray `p`, since Terminal.app does
not parse that either. The decision keeps the plain drag, so the view never
reports the mouse, and gives the wheel to the view through the two terminal
modes below.

## Decision

The view never reports the mouse, on any screen. As it starts it turns on
application cursor keys, which it resets when it ends, as terminfo's `rmkx`
does; and it sets xterm's alternate scroll mode, saving the
terminal's state first (XTSAVE) and restoring it (XTRESTORE) when it ends,
for terminals that need that mode; it sends no mode query. The alternate
scroll exit is a restore alone, not a reset: a terminal without XTSAVE,
Windows Terminal among them, ignores the restore and keeps the mode as its
own setting left it, where a reset would have turned a default-on setting
off for the rest of the tab. Application cursor keys change the arrow keys'
encoding to SS3 (`ESC O A`), which Bubble Tea decodes as the same keys.
Clicking a row no longer does anything: ↑ and ↓ move, Enter, → or `l` opens
the details, as before.

The costs view is unchanged: it reports the mouse on every screen and never
switches, which Terminal.app handles.

## Consequences

- The wheel moves the list, scrolls the details and moves the stop menu's
  choice, as the arrow keys do: in Terminal.app through application cursor
  keys (probed), and in xterm, Windows Terminal, Ghostty and VTE terminals,
  which have alternate scroll (vtdn, below) and get it from the view.
- A plain drag selects text on every screen, with no key held and no
  terminal setting changed.
- A click on a row does nothing; the view is keyboard-driven, as `less` and most
  full-screen terminal tools are.
- A wheel step over the stop menu moves its choice between `Stop` and
  `Force kill now`; Enter is still needed, so a flick stops nothing by
  itself.
- xterm, where alternate scroll is off by default, has it on while the view
  runs and off again after it; a terminal without XTSAVE keeps its own
  setting; a terminal with neither alternate scroll nor Terminal.app's
  application cursor rule scrolls its window under the wheel, as it does
  for `less`.

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
- [iTerm2 issue 2464, "Application cursor key mode for mouse wheel"](https://gitlab.com/gnachman/iterm2/-/issues/2464):
  a 2013 report that Terminal.app with the MouseTerm plug-in, and
  gnome-terminal, send the wheel as cursor keys to programs in application
  key mode; stock Terminal.app 2.15 was probed to do the same with DECCKM.
- [Windows Terminal issue 13187](https://github.com/microsoft/terminal/issues/13187):
  every terminal tested that has alternate scroll has it on by default,
  except xterm; Windows Terminal turned it on by default in PR 16535.
- [Windows Terminal issue 10153](https://github.com/microsoft/terminal/issues/10153):
  XTSAVE and XTRESTORE are not implemented there.
