// Package textfield provides kute's single-line text editor. Bubbles'
// textinput has a horizontal viewport but no text selection, while its
// textarea has selection but a pointer-backed, wrapping viewport whose View
// mutates state. This small value model supplies the subset kute needs while
// keeping render paths pure.
package textfield

import (
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// StyleState contains the styles that change with focus.
type StyleState struct {
	Text        lipgloss.Style
	Placeholder lipgloss.Style
	Prompt      lipgloss.Style
}

// Styles contains all visual styles for a field.
type Styles struct {
	Focused   StyleState
	Blurred   StyleState
	Cursor    lipgloss.Style
	Selection lipgloss.Style
}

// KeyMap is the editing grammar shared by every kute field.
type KeyMap struct {
	CharacterForward, CharacterBackward   key.Binding
	WordForward, WordBackward             key.Binding
	DeleteWordBackward, DeleteWordForward key.Binding
	DeleteAfterCursor, DeleteBeforeCursor key.Binding
	DeleteBackward, DeleteForward         key.Binding
	LineStart, LineEnd                    key.Binding
	SelectForward, SelectBackward         key.Binding
	SelectWordForward, SelectWordBackward key.Binding
	SelectToStart, SelectToEnd            key.Binding
	CopySelection                         key.Binding
}

// DefaultKeyMap preserves Bubbles textinput's navigation and deletion keys
// and adds the selection chords used by Bubbles textarea.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		CharacterForward:  key.NewBinding(key.WithKeys("right", "ctrl+f")),
		CharacterBackward: key.NewBinding(key.WithKeys("left", "ctrl+b")),
		WordForward:       key.NewBinding(key.WithKeys("alt+right", "ctrl+right", "alt+f")),
		WordBackward:      key.NewBinding(key.WithKeys("alt+left", "ctrl+left", "alt+b")),

		DeleteWordBackward: key.NewBinding(key.WithKeys("alt+backspace", "ctrl+w", "ctrl+backspace")),
		DeleteWordForward:  key.NewBinding(key.WithKeys("alt+delete", "alt+d", "ctrl+delete")),
		DeleteAfterCursor:  key.NewBinding(key.WithKeys("ctrl+k")),
		DeleteBeforeCursor: key.NewBinding(key.WithKeys("ctrl+u")),
		DeleteBackward:     key.NewBinding(key.WithKeys("backspace", "ctrl+h")),
		DeleteForward:      key.NewBinding(key.WithKeys("delete", "ctrl+d")),
		LineStart:          key.NewBinding(key.WithKeys("home", "ctrl+a")),
		LineEnd:            key.NewBinding(key.WithKeys("end", "ctrl+e")),

		SelectForward:      key.NewBinding(key.WithKeys("shift+right")),
		SelectBackward:     key.NewBinding(key.WithKeys("shift+left")),
		SelectWordForward:  key.NewBinding(key.WithKeys("ctrl+shift+right", "alt+shift+right", "alt+shift+f")),
		SelectWordBackward: key.NewBinding(key.WithKeys("ctrl+shift+left", "alt+shift+left", "alt+shift+b")),
		SelectToStart:      key.NewBinding(key.WithKeys("shift+home")),
		SelectToEnd:        key.NewBinding(key.WithKeys("shift+end")),
		CopySelection:      key.NewBinding(key.WithKeys("ctrl+shift+c")),
	}
}

// Model is a focused or blurred single-line editing buffer. Selection and
// cursor positions are rune indexes. offset is the first rune in the current
// horizontal viewport.
type Model struct {
	Prompt      string
	Placeholder string
	// EndOfBufferCharacter is drawn as the cursor when it sits after the
	// value. A space gives the usual reverse-video block; confirmation modals
	// use █ to preserve their established plain-text fallback.
	EndOfBufferCharacter rune
	CharLimit            int
	KeyMap               KeyMap

	styles Styles
	value  []rune
	width  int
	// fixedWidth retains textinput's SetWidth padding behavior. ViewWidth is
	// a max-width render helper for inline cells and deliberately leaves it
	// false so notes can follow immediately after short values.
	fixedWidth bool
	focus      bool
	pos        int
	offset     int

	selectionAnchor int
	selectionHead   int
	selecting       bool
}

// New returns an empty field with no prompt. Screens render their own prompt.
func New() Model { return Model{KeyMap: DefaultKeyMap(), EndOfBufferCharacter: ' '} }

func (m Model) Styles() Styles       { return m.styles }
func (m *Model) SetStyles(s Styles)  { m.styles = s }
func (m Model) Width() int           { return m.width }
func (m Model) Value() string        { return string(m.value) }
func (m Model) Position() int        { return m.pos }
func (m Model) Focused() bool        { return m.focus }
func (m Model) HasSelection() bool   { return m.selecting && m.selectionAnchor != m.selectionHead }
func (m Model) SelectedText() string { start, end := m.Selection(); return string(m.value[start:end]) }

// Selection returns the normalized selected rune range. With no selection it
// returns the cursor position twice.
func (m Model) Selection() (int, int) {
	if !m.HasSelection() {
		return m.pos, m.pos
	}
	return min(m.selectionAnchor, m.selectionHead), max(m.selectionAnchor, m.selectionHead)
}

// SetWidth sets the maximum visible content width in terminal cells.
func (m *Model) SetWidth(width int) {
	m.width = max(width, 0)
	m.fixedWidth = true
	m.ensureCursorVisible()
}

// ViewWidth renders a width-constrained copy, keeping View itself usable by
// fixed-width callers and preserving render purity.
func (m Model) ViewWidth(width int) string {
	m.SetWidth(width)
	m.fixedWidth = false
	return m.View()
}

// SetValue replaces the buffer with sanitized single-line text.
func (m *Model) SetValue(value string) {
	wasEmpty := len(m.value) == 0
	runes := sanitize([]rune(value))
	if m.CharLimit > 0 && len(runes) > m.CharLimit {
		runes = runes[:m.CharLimit]
	}
	m.value = runes
	if wasEmpty || m.pos > len(runes) {
		m.pos = len(runes)
	}
	m.clearSelection()
	m.ensureCursorVisible()
}

func (m *Model) SetCursor(pos int) {
	m.pos = clamp(pos, 0, len(m.value))
	m.clearSelection()
	m.ensureCursorVisible()
}

func (m *Model) CursorStart() { m.SetCursor(0) }
func (m *Model) CursorEnd()   { m.SetCursor(len(m.value)) }

func (m *Model) Focus() tea.Cmd { m.focus = true; return nil }
func (m *Model) Blur()          { m.focus = false }

func (m *Model) Reset() {
	m.value = nil
	m.pos, m.offset = 0, 0
	m.clearSelection()
}

// Update applies one editing message.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) { //nolint:gocyclo // Editing commands are clearer as one key-dispatch switch.
	if !m.focus {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.PasteMsg:
		m.insert([]rune(msg.Content))
		return m, nil
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.KeyMap.CopySelection):
			if m.HasSelection() {
				return m, tea.SetClipboard(m.SelectedText())
			}
		case key.Matches(msg, m.KeyMap.SelectWordBackward):
			m.beginSelection()
			m.pos = m.wordBackwardPosition()
			m.finishSelectionMove()
		case key.Matches(msg, m.KeyMap.SelectWordForward):
			m.beginSelection()
			m.pos = m.wordForwardPosition()
			m.finishSelectionMove()
		case key.Matches(msg, m.KeyMap.SelectBackward):
			m.beginSelection()
			m.pos = max(m.pos-1, 0)
			m.finishSelectionMove()
		case key.Matches(msg, m.KeyMap.SelectForward):
			m.beginSelection()
			m.pos = min(m.pos+1, len(m.value))
			m.finishSelectionMove()
		case key.Matches(msg, m.KeyMap.SelectToStart):
			m.beginSelection()
			m.pos = 0
			m.finishSelectionMove()
		case key.Matches(msg, m.KeyMap.SelectToEnd):
			m.beginSelection()
			m.pos = len(m.value)
			m.finishSelectionMove()
		case key.Matches(msg, m.KeyMap.DeleteWordBackward):
			if !m.deleteSelection() {
				start := m.wordBackwardPosition()
				m.value = append(m.value[:start], m.value[m.pos:]...)
				m.pos = start
			}
		case key.Matches(msg, m.KeyMap.DeleteWordForward):
			if !m.deleteSelection() {
				end := m.wordForwardPosition()
				m.value = append(m.value[:m.pos], m.value[end:]...)
			}
		case key.Matches(msg, m.KeyMap.DeleteAfterCursor):
			if !m.deleteSelection() {
				m.value = m.value[:m.pos]
			}
		case key.Matches(msg, m.KeyMap.DeleteBeforeCursor):
			if !m.deleteSelection() {
				m.value = append([]rune(nil), m.value[m.pos:]...)
				m.pos = 0
			}
		case key.Matches(msg, m.KeyMap.DeleteBackward):
			if !m.deleteSelection() && m.pos > 0 {
				m.value = append(m.value[:m.pos-1], m.value[m.pos:]...)
				m.pos--
			}
		case key.Matches(msg, m.KeyMap.DeleteForward):
			if !m.deleteSelection() && m.pos < len(m.value) {
				m.value = append(m.value[:m.pos], m.value[m.pos+1:]...)
			}
		case key.Matches(msg, m.KeyMap.WordBackward):
			if m.HasSelection() {
				m.pos, _ = m.Selection()
			} else {
				m.pos = m.wordBackwardPosition()
			}
			m.clearSelection()
		case key.Matches(msg, m.KeyMap.WordForward):
			if m.HasSelection() {
				_, m.pos = m.Selection()
			} else {
				m.pos = m.wordForwardPosition()
			}
			m.clearSelection()
		case key.Matches(msg, m.KeyMap.CharacterBackward):
			if m.HasSelection() {
				m.pos, _ = m.Selection()
			} else {
				m.pos = max(m.pos-1, 0)
			}
			m.clearSelection()
		case key.Matches(msg, m.KeyMap.CharacterForward):
			if m.HasSelection() {
				_, m.pos = m.Selection()
			} else {
				m.pos = min(m.pos+1, len(m.value))
			}
			m.clearSelection()
		case key.Matches(msg, m.KeyMap.LineStart):
			m.pos = 0
			m.clearSelection()
		case key.Matches(msg, m.KeyMap.LineEnd):
			m.pos = len(m.value)
			m.clearSelection()
		default:
			if msg.Text != "" {
				m.insert([]rune(msg.Text))
			}
		}
	}
	m.ensureCursorVisible()
	return m, nil
}

// View renders the currently visible slice. Selection replaces the cursor as
// the active editing affordance, matching Bubbles textarea.
func (m Model) View() string {
	styles := m.styles.Blurred
	if m.focus {
		styles = m.styles.Focused
	}
	prompt := styles.Prompt.Render(m.Prompt)

	if len(m.value) == 0 && m.Placeholder != "" {
		visible := []rune(m.Placeholder)
		if m.width > 0 {
			visible = fitPrefix(visible, m.width)
		}
		if !m.focus || len(visible) == 0 {
			return prompt + m.pad(styles.Placeholder.Render(string(visible)), styles.Placeholder)
		}
		content := m.styles.Cursor.Inherit(styles.Placeholder).Reverse(true).Render(string(visible[0])) +
			styles.Placeholder.Render(string(visible[1:]))
		return prompt + m.pad(content, styles.Placeholder)
	}

	start, end := m.visibleRange()
	selectionStart, selectionEnd := m.Selection()
	var out strings.Builder
	for i := start; i < end; {
		style := styles.Text
		j := i + 1
		switch {
		case m.HasSelection() && i >= selectionStart && i < selectionEnd:
			style = m.styles.Selection
			for j < end && j >= selectionStart && j < selectionEnd {
				j++
			}
		case m.focus && !m.HasSelection() && i == m.pos:
			style = m.styles.Cursor.Reverse(true)
		default:
			for j < end {
				if (m.HasSelection() && j >= selectionStart && j < selectionEnd) ||
					(m.focus && !m.HasSelection() && j == m.pos) {
					break
				}
				j++
			}
		}
		out.WriteString(style.Render(string(m.value[i:j])))
		i = j
	}
	if m.focus && !m.HasSelection() && m.pos == len(m.value) {
		cursor := m.EndOfBufferCharacter
		if cursor == 0 {
			cursor = ' '
		}
		cursorStyle := m.styles.Cursor
		if cursor == ' ' {
			cursorStyle = cursorStyle.Reverse(true)
		}
		out.WriteString(cursorStyle.Render(string(cursor)))
	}
	return prompt + m.pad(out.String(), styles.Text)
}

func (m Model) pad(content string, style lipgloss.Style) string {
	if m.width <= 0 || !m.fixedWidth {
		return content
	}
	return content + style.Render(strings.Repeat(" ", max(m.width-lipgloss.Width(content), 0)))
}

func (m *Model) beginSelection() {
	if !m.selecting {
		m.selectionAnchor = m.pos
	}
	m.selecting = true
}

func (m *Model) finishSelectionMove() {
	m.selectionHead = m.pos
	m.ensureCursorVisible()
}

func (m *Model) clearSelection() {
	m.selecting = false
	m.selectionAnchor, m.selectionHead = m.pos, m.pos
}

func (m *Model) deleteSelection() bool {
	if !m.HasSelection() {
		return false
	}
	start, end := m.Selection()
	m.value = append(m.value[:start], m.value[end:]...)
	m.pos = start
	m.clearSelection()
	return true
}

func (m *Model) insert(input []rune) {
	input = sanitize(input)
	m.deleteSelection()
	if m.CharLimit > 0 {
		available := m.CharLimit - len(m.value)
		if available <= 0 {
			return
		}
		input = input[:min(len(input), available)]
	}
	tail := append([]rune(nil), m.value[m.pos:]...)
	m.value = append(m.value[:m.pos], input...)
	m.pos += len(input)
	m.value = append(m.value, tail...)
	m.clearSelection()
}

func (m Model) wordBackwardPosition() int {
	pos := m.pos
	for pos > 0 && unicode.IsSpace(m.value[pos-1]) {
		pos--
	}
	for pos > 0 && !unicode.IsSpace(m.value[pos-1]) {
		pos--
	}
	return pos
}

func (m Model) wordForwardPosition() int {
	pos := m.pos
	for pos < len(m.value) && unicode.IsSpace(m.value[pos]) {
		pos++
	}
	for pos < len(m.value) && !unicode.IsSpace(m.value[pos]) {
		pos++
	}
	return pos
}

func (m *Model) ensureCursorVisible() {
	m.pos = clamp(m.pos, 0, len(m.value))
	if m.width <= 0 || displayWidth(m.value)+boolWidth(m.focus && m.pos == len(m.value)) <= m.width {
		m.offset = 0
		return
	}
	m.offset = clamp(m.offset, 0, m.pos)
	for m.offset < m.pos && !cursorFits(m.value, m.offset, m.pos, m.width) {
		m.offset++
	}
	if m.pos < m.offset {
		m.offset = m.pos
	}
}

func (m Model) visibleRange() (int, int) {
	if m.width <= 0 {
		return 0, len(m.value)
	}
	start := clamp(m.offset, 0, len(m.value))
	if !cursorFits(m.value, start, m.pos, m.width) {
		start = min(m.pos, len(m.value))
		for start > 0 && cursorFits(m.value, start-1, m.pos, m.width) {
			start--
		}
	}
	used, end := 0, start
	for end < len(m.value) {
		w := runewidth.RuneWidth(m.value[end])
		if used+w > m.width {
			break
		}
		used += w
		end++
	}
	return start, end
}

func cursorFits(value []rune, start, pos, width int) bool {
	if width <= 0 || pos < start {
		return width <= 0
	}
	used := displayWidth(value[start:pos])
	if pos < len(value) {
		used += max(runewidth.RuneWidth(value[pos]), 1)
	} else {
		used++
	}
	return used <= width
}

func fitPrefix(value []rune, width int) []rune {
	used := 0
	for i, r := range value {
		used += runewidth.RuneWidth(r)
		if used > width {
			return value[:i]
		}
	}
	return value
}

func displayWidth(value []rune) int { return uniseg.StringWidth(string(value)) }
func boolWidth(ok bool) int {
	if ok {
		return 1
	}
	return 0
}

func sanitize(input []rune) []rune {
	out := make([]rune, 0, len(input))
	for _, r := range input {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			out = append(out, ' ')
		case r == unicode.ReplacementChar || unicode.IsControl(r):
			continue
		default:
			out = append(out, r)
		}
	}
	return out
}

func clamp(value, low, high int) int { return min(max(value, low), high) }
