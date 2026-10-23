package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/resources"
)

func TestMotionKeysMoveFocusedPanelSelection(t *testing.T) {
	base := goldenOverviewModel(t, 120, 36)
	base.focus = panelNodes
	base.nodeTrouble = make([]resources.Row, 20)
	// A panel renders at most maxPanelRows, so that's both its page and
	// how far down the cursor can go.
	page, last := maxPanelRows, maxPanelRows-1
	for _, tc := range []struct {
		name string
		from int
		key  tea.KeyPressMsg
		want int
	}{
		{"G", 0, tea.KeyPressMsg{Text: "G", Code: 'G'}, last},
		{"end", 0, tea.KeyPressMsg{Code: tea.KeyEnd}, last},
		{"home", 4, tea.KeyPressMsg{Code: tea.KeyHome}, 0},
		{"ctrl+d", 0, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, page / 2},
		{"ctrl+f", 0, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}, last},
		{"ctrl+e", 0, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.nodesSel = tc.from
			m = step(t, m, tc.key)
			if m.nodesSel != tc.want {
				t.Fatalf("nodesSel = %d, want %d", m.nodesSel, tc.want)
			}
		})
	}
}
