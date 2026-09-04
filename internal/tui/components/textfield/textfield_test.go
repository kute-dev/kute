package textfield

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

func testField(value string, width int) Model {
	m := New()
	m.SetStyles(Styles{
		Focused:   StyleState{Text: lipgloss.NewStyle()},
		Blurred:   StyleState{Text: lipgloss.NewStyle()},
		Cursor:    lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		Selection: lipgloss.NewStyle().Reverse(true),
	})
	m.SetWidth(width)
	m.SetValue(value)
	m.CursorEnd()
	m.Focus()
	return m
}

func press(m Model, code rune, mod tea.KeyMod) Model {
	m, _ = m.Update(tea.KeyPressMsg{Code: code, Mod: mod})
	return m
}

func TestViewportAlwaysContainsCursor(t *testing.T) {
	m := testField("abcdefghijklmnop", 6)
	if got := uniseg.StringWidth(ansi.Strip(m.View())); got > 6 {
		t.Fatalf("end viewport width = %d, want <= 6", got)
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "mnop") {
		t.Fatalf("end viewport %q does not show the tail", got)
	}

	m = press(m, tea.KeyLeft, 0)
	m = press(m, tea.KeyLeft, 0)
	if m.Position() != 14 || m.offset == 0 {
		t.Fatalf("cursor/offset after left = %d/%d, want cursor 14 in a scrolled viewport", m.Position(), m.offset)
	}

	m.CursorStart()
	if got := ansi.Strip(m.View()); !strings.HasPrefix(got, "abcdef") {
		t.Fatalf("start viewport = %q, want beginning of value", got)
	}

	beforeOffset, beforePosition := m.offset, m.pos
	_ = m.ViewWidth(3)
	if m.offset != beforeOffset || m.pos != beforePosition {
		t.Fatalf("ViewWidth mutated model: offset/pos %d/%d became %d/%d", beforeOffset, beforePosition, m.offset, m.pos)
	}
}

func TestViewportUsesTerminalCellWidths(t *testing.T) {
	m := testField("ab界cd界ef", 5)
	view := ansi.Strip(m.View())
	if got := uniseg.StringWidth(view); got > 5 {
		t.Fatalf("wide-rune viewport %q is %d cells, want <= 5", view, got)
	}
	if !strings.Contains(view, "ef") {
		t.Fatalf("wide-rune viewport %q does not contain the cursor tail", view)
	}
}

func TestKeyboardSelectionAndReplacement(t *testing.T) {
	m := testField("hello world", 20)
	for range 5 {
		m = press(m, tea.KeyLeft, tea.ModShift)
	}
	if got := m.SelectedText(); got != "world" {
		t.Fatalf("backward selection = %q, want world", got)
	}
	selectedView := m.View()
	m.clearSelection()
	if selectedView == m.View() {
		t.Fatal("selection style did not change rendered output")
	}

	m.CursorEnd()
	m = press(m, tea.KeyLeft, tea.ModShift|tea.ModCtrl)
	if got := m.SelectedText(); got != "world" {
		t.Fatalf("word selection = %q, want world", got)
	}
	m, _ = m.Update(tea.KeyPressMsg{Text: "kute"})
	if got := m.Value(); got != "hello kute" {
		t.Fatalf("typing over selection = %q, want hello kute", got)
	}

	m.CursorStart()
	m = press(m, tea.KeyEnd, tea.ModShift)
	m, _ = m.Update(tea.PasteMsg{Content: "new\nvalue"})
	if got := m.Value(); got != "new value" {
		t.Fatalf("paste over selection = %q, want a sanitized single line", got)
	}
}

func TestSelectionDeletionCollapseAndCopy(t *testing.T) {
	m := testField("abcdef", 10)
	m = press(m, tea.KeyLeft, tea.ModShift)
	m = press(m, tea.KeyLeft, tea.ModShift)
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModShift}); cmd == nil {
		t.Fatal("ctrl+shift+c did not return a clipboard command")
	}
	m = press(m, tea.KeyBackspace, 0)
	if got := m.Value(); got != "abcd" {
		t.Fatalf("backspace over selection = %q, want abcd", got)
	}

	m.CursorStart()
	m = press(m, tea.KeyRight, tea.ModShift)
	m = press(m, tea.KeyRight, tea.ModShift)
	m = press(m, tea.KeyRight, 0)
	if m.HasSelection() || m.Position() != 2 {
		t.Fatalf("right collapse left selection active at %d, want no selection at end 2", m.Position())
	}
}

func TestCharLimitAndSanitization(t *testing.T) {
	m := testField("", 10)
	m.CharLimit = 5
	m, _ = m.Update(tea.PasteMsg{Content: "ab\tcd\nef"})
	if got := m.Value(); got != "ab cd" {
		t.Fatalf("limited sanitized paste = %q, want ab cd", got)
	}
}
