package cli

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// press handles a key. ctrl+c always quits. While find's input is open, the
// keys of design 3.2 go to it (see pressFind); otherwise q quits, and the
// screen shown takes the key.
func (m *guardView) press(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch {
	case key == "ctrl+c":
		return tea.Quit
	case m.find.open && m.mode == listMode:
		return m.pressFind(msg)
	case key == "q":
		return tea.Quit
	}
	switch m.mode {
	case menuMode:
		return m.pressMenu(key)
	case detailMode:
		return m.pressDetails(msg)
	case listMode:
	}
	return m.pressList(key)
}

// pressList handles a key on the list. There is no j or k to move, since k
// stops, as in htop. esc clears a kept filter, and quits when none is kept.
func (m *guardView) pressList(key string) tea.Cmd {
	last, page := max(len(m.rows)-1, 0), max(m.page, 1)
	switch key {
	case "up", "down", "pgup", "pgdown", "home", "end", "space", "enter", "right", "l", "k":
		m.moved = true
	}
	switch key {
	case "up":
		m.cursor = max(m.cursor-1, 0)
	case "down":
		m.cursor = min(m.cursor+1, last)
	case "pgup":
		m.cursor = max(m.cursor-page, 0)
	case "pgdown":
		m.cursor = min(m.cursor+page, last)
	case "home":
		m.cursor = 0
	case "end":
		m.cursor = last
	case "space":
		if m.cursor < len(m.rows) && active(*m.rows[m.cursor].view) {
			id := m.rows[m.cursor].view.ID
			if m.tagged[id] {
				delete(m.tagged, id)
			} else {
				m.tagged[id] = true
			}
		}
		m.cursor = min(m.cursor+1, last)
	case "enter", "right", "l":
		if m.cursor < len(m.rows) {
			m.openDetails()
		}
	case "k":
		m.openMenu(listMode)
	case "s":
		m.byPeak = !m.byPeak
		m.rebuild()
		m.cursor, m.first = m.startRow(), 0
	case "/":
		m.find.open = true
		m.find.input.SetValue(m.find.text)
		m.find.input.CursorEnd()
		return m.find.input.Focus()
	case "esc":
		if m.find.text != "" {
			return m.ask("")
		}
		return tea.Quit
	}
	return nil
}

// pressFind handles a key while find's input is open: enter closes it and
// keeps the filter, esc closes it and clears the filter, ↑, ↓, pgup and pgdn
// move the selection, and every other key edits the text.
func (m *guardView) pressFind(msg tea.KeyPressMsg) tea.Cmd {
	switch key := msg.String(); key {
	case "enter":
		m.find.open = false
		m.find.input.Blur()
	case "esc":
		m.find.open = false
		m.find.input.Blur()
		return m.ask("")
	case "up", "down", "pgup", "pgdown":
		return m.pressList(key)
	default:
		return m.edit(msg)
	}
	return nil
}

// edit hands a key or a paste to find's input, and asks the history again
// when its text changed.
func (m *guardView) edit(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.find.input, cmd = m.find.input.Update(msg)
	if text := m.find.input.Value(); text != m.find.text {
		return tea.Batch(cmd, m.ask(text))
	}
	return cmd
}

// pressDetails handles a key on the details. With the side panel open, ↑,
// ↓, pgup, pgdn, home and end scroll it and esc or ← closes it. Without it,
// ↑ and ↓ choose among the `… more` rows when there are any, enter or →
// opens the chosen one in the panel, and the keys scroll the details
// otherwise; esc or ← goes back to the list. k stops on both.
func (m *guardView) pressDetails(msg tea.KeyPressMsg) tea.Cmd {
	d, key := &m.detail, msg.String()
	pane := &m.details
	if d.panel {
		pane = &m.pane
	}
	picks := len(d.picks)
	switch {
	case key == "k":
		m.openMenu(detailMode)
	case key == "home":
		pane.GotoTop()
	case key == "end":
		pane.GotoBottom()
	case (key == "esc" || key == "left" || key == "h") && d.panel:
		d.panel, d.reveal = false, true
	case key == "esc" || key == "left" || key == "h":
		m.mode = listMode
	case d.panel || picks == 0:
		var cmd tea.Cmd
		*pane, cmd = pane.Update(msg)
		return cmd
	case key == "up":
		d.pick, d.reveal = max(min(d.pick, picks-1)-1, 0), true
	case key == "down":
		d.pick, d.reveal = min(d.pick+1, picks-1), true
	case key == "enter" || key == "right" || key == "l":
		d.pick = min(d.pick, picks-1)
		d.panel, d.part = true, d.picks[d.pick].part
		m.pane.GotoTop()
	default:
		var cmd tea.Cmd
		m.details, cmd = m.details.Update(msg)
		return cmd
	}
	return nil
}

// pressMenu handles a key in the stop menu: ↑ and ↓ choose, enter sends and
// esc or ← cancels.
func (m *guardView) pressMenu(key string) tea.Cmd {
	switch key {
	case "up":
		m.menu = max(m.menu-1, 0)
	case "down":
		m.menu = min(m.menu+1, len(stopChoices)-1)
	case "esc", "left", "h":
		m.mode = m.menuFrom
	case "enter":
		m.mode = m.menuFrom
		ids := m.running(m.targets)
		var gone []string
		for _, id := range m.targets {
			if !slices.Contains(ids, id) {
				gone = append(gone, id)
			}
		}
		cmd := m.send(ids, m.menu == 1)
		if len(gone) > 0 {
			m.say(strings.TrimPrefix(m.notice+"; ", "; ") + m.shortIDs(gone) + " already finished")
		}
		return cmd
	}
	return nil
}

// openDetails shows the selected command's details from their top, with
// the parts the snapshot sent for a finished command the history does not
// hold yet, the first `… more` row chosen and the side panel closed.
func (m *guardView) openDetails() {
	r := m.rows[m.cursor]
	m.mode = detailMode
	m.detail = viewDetail{id: r.view.ID, parts: r.finished}
	m.details.GotoTop()
}

// stopTargets are the IDs a stop would name: from the details, the row
// shown; from the list, the tagged rows still running, or else the selected
// one when it runs.
func (m *guardView) stopTargets(from viewMode) []string {
	var ids []string
	if from == listMode {
		for _, c := range m.snap.Commands {
			if m.tagged[c.ID] && active(c) {
				ids = append(ids, c.ID)
			}
		}
	}
	if len(ids) == 0 && m.cursor < len(m.rows) && active(*m.rows[m.cursor].view) {
		ids = []string{m.rows[m.cursor].view.ID}
	}
	return ids
}

// openMenu opens the stop menu from a screen, its polite stop chosen, when
// there is a running row to stop and a watcher to ask.
func (m *guardView) openMenu(from viewMode) {
	ids := m.stopTargets(from)
	switch {
	case len(ids) == 0 && m.cursor < len(m.rows):
		m.say(m.label(m.selectedID()) + " already finished")
	case len(ids) == 0:
	case !m.connected:
		m.say("No watcher is connected; nothing was stopped")
	default:
		m.mode, m.menu, m.menuFrom, m.targets = menuMode, 0, from, ids
	}
}
