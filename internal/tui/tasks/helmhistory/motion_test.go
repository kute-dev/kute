package helmhistory

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

func TestMotionKeysMoveSelection(t *testing.T) {
	base := goldenHelmHistoryModel(t, 120, 36)
	base.revisions = make([]kube.HelmRelease, 60)
	page, last := base.tableDataRows(), len(base.revisions)-1
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
			if m.selected < m.offset || m.selected >= m.offset+page {
				t.Fatalf("selected %d outside window [%d, %d)", m.selected, m.offset, m.offset+page)
			}
		})
	}
}
