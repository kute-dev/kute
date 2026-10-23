package secretdata

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMotionKeysMoveSelection(t *testing.T) {
	base := goldenSecretDataModel(t, 120, 36)
	base.keys = make([]secretKeyRow, 60)
	page, last := motionPageRows, len(base.keys)-1
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
