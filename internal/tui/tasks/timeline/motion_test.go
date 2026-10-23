package timeline

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

func TestMotionKeysMoveFeedSelection(t *testing.T) {
	entries := make([]kube.TimelineEntry, 60)
	for i := range entries {
		entries[i] = kube.TimelineEntry{
			Time: goldenNow.Add(-time.Duration(i) * time.Minute), Kind: kube.TimelineRestart,
			Object: fmt.Sprintf("Pod/p-%02d", i), Namespace: "default", Reason: "Restarted", Message: "app · exit 1",
		}
	}
	m0 := New(Config{Session: newSession(), Events: fakeEvents{}, Namespace: "default"})
	m0.SetSize(120, 36)
	base := loadFixedFeed(m0, entries, nil, "")
	page, _ := base.feedViewport()
	last := len(base.rows) - 1
	if page < 4 || last <= page {
		t.Fatalf("fixture too small: page %d, last %d", page, last)
	}
	// Every row here is one line, so a line-budgeted page is a row count.
	for _, tc := range []struct {
		name string
		from int
		key  tea.KeyPressMsg
		want int
	}{
		{"G", 0, tea.KeyPressMsg{Text: "G", Code: 'G'}, last},
		{"end", 0, tea.KeyPressMsg{Code: tea.KeyEnd}, last},
		{"home", 5, tea.KeyPressMsg{Code: tea.KeyHome}, 0},
		{"ctrl+d", 0, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, page / 2},
		{"ctrl+f", 0, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}, page},
		{"ctrl+e", 0, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.selected = tc.from
			m = step(t, m, tc.key)
			if m.selected != tc.want {
				t.Fatalf("selected = %d, want %d (page %d)", m.selected, tc.want, page)
			}
		})
	}
}

func TestMotionKeysMoveFocusedRail(t *testing.T) {
	m := goldenObjectModel(120, 36)
	if len(m.rail) < 2 {
		t.Fatalf("fixture rail has %d revisions, want at least 2", len(m.rail))
	}
	m.railFocused = true
	m = step(t, m, tea.KeyPressMsg{Text: "G", Code: 'G'})
	if m.railSelected != len(m.rail)-1 {
		t.Fatalf("G on the focused rail: railSelected = %d, want %d", m.railSelected, len(m.rail)-1)
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	if m.railSelected != 0 {
		t.Fatalf("home on the focused rail: railSelected = %d, want 0", m.railSelected)
	}
}
