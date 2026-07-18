package yamlview

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case kube.ResourceChangedMsg:
		if msg.Kind == m.kind && m.lister != nil {
			return m, m.load()
		}
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
	case loadedMsg:
		return m.applyLoaded(msg)
	case components.SpinnerTickMsg:
		if m.state != tui.TaskStateLoading {
			return m, nil
		}
		m.spinner = m.spinner.Advance()
		return m, components.SpinnerTick()
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *Model) applyLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.state = tui.TaskStateError
		if kube.IsPermissionError(msg.err) {
			m.state = tui.TaskStatePermissionDenied
		}
		m.feedback = msg.err.Error()
		return m, nil
	}
	m.lines = strings.Split(msg.text, "\n")
	m.resourceVersion = msg.resourceVersion
	m.managedFieldsLines = splitManagedFieldsLines(msg.managedFieldsYAML)
	if m.folded == nil {
		m.folded = defaultFolds(m.lines)
		if len(m.managedFieldsLines) > 0 {
			m.folded["managedFields"] = true
		}
	}
	if m.kind == kube.KindSecret {
		m.isSecret = true
		m.secretType = parseSecretType(m.lines)
		m.secretData = parseSecretData(m.lines)
		if m.revealed == nil {
			m.revealed = map[string]bool{}
		}
	}
	n := len(m.rendered())
	if m.cursor >= n {
		m.cursor = max(n-1, 0)
	}
	m.clampOffset()
	m.state = tui.TaskStateReady
	m.feedback = ""
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.searchActive {
		return m.updateSearchKey(msg)
	}
	if m.revealAllConfirm {
		return m.updateRevealAllConfirmKey(msg)
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "tab":
		if m.folded != nil {
			rendered := m.rendered()
			// FoldableKey (managedfields.go) marks an expanded header not
			// backed by a real Model.lines line — toggleFoldAtCursor's
			// topLevelBlocks lookup can't find it, so fold it directly.
			if m.cursor >= 0 && m.cursor < len(rendered) && rendered[m.cursor].FoldableKey != "" {
				m.folded[rendered[m.cursor].FoldableKey] = true
			} else {
				toggleFoldAtCursor(m.lines, m.folded, rendered, m.cursor)
			}
			m.clampCursor()
		}
	case "f":
		if m.folded != nil {
			unfoldAll(m.folded)
			m.clampCursor()
		}
	case "/":
		m.searchActive = true
		m.searchQuery = ""
	case "Y":
		return m, tea.SetClipboard(strings.Join(m.lines, "\n"))
	case "x":
		if m.isSecret {
			m.toggleRevealAtCursor()
		}
	case "X":
		if m.isSecret && m.hasUnrevealedSecretData() {
			m.revealAllConfirm = true
		}
	case "y":
		if m.isSecret {
			return m, m.copyDecodedSecretValue()
		}
	}
	return m, nil
}

// toggleRevealAtCursor flips the mask/reveal state of the data: entry the
// cursor is currently on (a no-op everywhere else — most lines carry no
// SecretKey). Docs/design README.md §21a: "x reveals/masks the cursor key
// in place".
func (m *Model) toggleRevealAtCursor() {
	rendered := m.rendered()
	if m.cursor < 0 || m.cursor >= len(rendered) {
		return
	}
	key := rendered[m.cursor].SecretKey
	if key == "" {
		return
	}
	if m.revealed == nil {
		m.revealed = map[string]bool{}
	}
	m.revealed[key] = !m.revealed[key]
	m.clampCursor()
}

func (m *Model) hasUnrevealedSecretData() bool {
	for _, e := range m.secretData {
		if !m.revealed[e.key] {
			return true
		}
	}
	return false
}

// updateRevealAllConfirmKey routes keys while the "X reveal all" inline y/N
// gate is showing (§21a: "X reveals all behind an inline y/N").
func (m *Model) updateRevealAllConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if m.revealed == nil {
			m.revealed = map[string]bool{}
		}
		for _, e := range m.secretData {
			m.revealed[e.key] = true
		}
	}
	m.revealAllConfirm = false
	return m, nil
}

// revealAllConfirmPrompt renders the confirm keybar's RightNote — mirrors
// browse/edit.go's editConfirmPrompt shape for this package's own bespoke
// (non-actions.Controller) inline gate.
func (m Model) revealAllConfirmPrompt() string {
	return fmt.Sprintf("Reveal all %d keys in this Secret? (y) confirm  (n) cancel", len(m.secretData))
}

// copyDecodedSecretValue copies the cursor's decoded plaintext — "the only
// plaintext export" (§21a) — regardless of whether it's currently masked or
// revealed on screen; decoding already happened at load, in memory only.
func (m Model) copyDecodedSecretValue() tea.Cmd {
	rendered := m.rendered()
	if m.cursor < 0 || m.cursor >= len(rendered) {
		return nil
	}
	key := rendered[m.cursor].SecretKey
	if key == "" {
		return nil
	}
	for _, e := range m.secretData {
		if e.key == key && e.decodeOK {
			return tea.SetClipboard(string(e.decoded))
		}
	}
	return nil
}

func (m *Model) updateSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searchActive = false
	case "enter":
		m.searchActive = false
	case "backspace":
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
			m.jumpToMatch()
		}
	case "up":
		m.moveCursor(-1)
	case "down":
		m.moveCursor(1)
	default:
		if msg.Text != "" {
			m.searchQuery += msg.Text
			m.jumpToMatch()
		}
	}
	return m, nil
}

// jumpToMatch moves the cursor to the nearest case-insensitive match at or
// after the current position, wrapping to the top if none is found below —
// mirrors browse's "/" filter UX (docs/design README.md §8a: "/ searches
// (jump + highlight)").
func (m *Model) jumpToMatch() {
	if m.searchQuery == "" {
		return
	}
	query := strings.ToLower(m.searchQuery)
	rendered := m.rendered()
	for i := m.cursor; i < len(rendered); i++ {
		if strings.Contains(strings.ToLower(rendered[i].Text), query) {
			m.cursor = i
			m.clampOffset()
			return
		}
	}
	for i := 0; i < m.cursor; i++ {
		if strings.Contains(strings.ToLower(rendered[i].Text), query) {
			m.cursor = i
			m.clampOffset()
			return
		}
	}
}

func (m *Model) moveCursor(delta int) {
	n := len(m.rendered())
	if n == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	m.cursor = clamp(m.cursor+delta, 0, n-1)
	m.clampOffset()
}

func (m *Model) clampCursor() {
	n := len(m.rendered())
	m.cursor = clamp(m.cursor, 0, max(n-1, 0))
	m.clampOffset()
}

// bodyRows is how many content rows the viewport shows — kept in sync with
// view.go's own body-height budget.
func (m Model) bodyRows() int {
	return max(tui.FrameBodyHeight(m.height, len(m.Strips(m.width))), 1)
}

func (m *Model) clampOffset() {
	rows := m.bodyRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
