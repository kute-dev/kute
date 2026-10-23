package jobattempts

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/resources"
)

func TestMotionKeysMoveSelection(t *testing.T) {
	c, _ := newFakeJob("default", "batch-1")
	base := newModel(t, c, "default", "batch-1")
	base.summary.Attempts = make([]resources.JobAttempt, 60)
	page, last := base.tableDataRows(), len(base.attemptList())-1
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
			m.selectedAttempt = tc.from
			upd, _ := m.Update(tc.key)
			m = *upd.(*Model)
			if m.selectedAttempt != tc.want {
				t.Fatalf("selectedAttempt = %d, want %d (page %d)", m.selectedAttempt, tc.want, page)
			}
			if m.selectedAttempt < m.offset || m.selectedAttempt >= m.offset+page {
				t.Fatalf("selectedAttempt %d outside window [%d, %d)", m.selectedAttempt, m.offset, m.offset+page)
			}
		})
	}
}
