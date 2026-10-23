package forwardpicker

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMotionKeysMoveSelection(t *testing.T) {
	base := goldenForwardPickerModel(t, 120, 36)
	base.rows = make([]portRow, 60)
	page, last := base.height, len(base.rows)-1
	if page < 4 || last <= page {
		t.Fatalf("fixture too small: page %d, last %d", page, last)
	}
	for _, tc := range []struct {
		name string
		from int
		key  tea.KeyPressMsg
		want int
	}{
		{"G", 0, tea.KeyPressMsg{Text: "G", Code: 'G'}, last},
		{"end", 0, tea.KeyPressMsg{Code: tea.KeyEnd}, last},
		{"home", 5, tea.KeyPressMsg{Code: tea.KeyHome}, 0},
		{"ctrl+d", 0, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, max(page/2, 1)},
		{"ctrl+f", 0, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}, min(page, last)},
		{"ctrl+e", 0, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.selected = tc.from
			upd, _ := m.Update(tc.key)
			m = *upd.(*Model)
			if m.selected != tc.want {
				t.Fatalf("selected = %d, want %d (page %d)", m.selected, tc.want, page)
			}
		})
	}
}

func TestMotionKeysDoNotMoveWhileEditingAPort(t *testing.T) {
	m := typeOnRow(t, goldenForwardPickerModel(t, 120, 36), "9")
	if !m.editing() {
		t.Fatal("expected a digit to open the row's port edit")
	}
	before := m.selected
	upd, _ := m.Update(tea.KeyPressMsg{Text: "G", Code: 'G'})
	m = *upd.(*Model)
	if m.selected != before || !m.editing() {
		t.Fatalf("G while editing moved the selection (%d → %d) or closed the edit", before, m.selected)
	}
}
