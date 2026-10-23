package poddetail

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

func TestMotionKeysMoveContainerSelection(t *testing.T) {
	base := goldenPodDetailModel(t, 120, 36)
	base.pod.ContainerInfos = make([]kube.ContainerInfo, 12)
	base.pod.InitContainerInfos, base.pod.EphemeralContainerInfos = nil, nil
	n := base.totalContainerRows()
	for _, tc := range []struct {
		name string
		from int
		key  tea.KeyPressMsg
		want int
	}{
		{"G", 0, tea.KeyPressMsg{Text: "G", Code: 'G'}, n - 1},
		{"end", 0, tea.KeyPressMsg{Code: tea.KeyEnd}, n - 1},
		{"home", 5, tea.KeyPressMsg{Code: tea.KeyHome}, 0},
		// The whole CONTAINERS grid is on screen, so half a page is half of it.
		{"ctrl+d", 0, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, n / 2},
		{"ctrl+u", 8, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, 8 - n/2},
		{"ctrl+e", 0, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.selectedContainer = tc.from
			m = step(t, m, tc.key)
			if m.selectedContainer != tc.want {
				t.Fatalf("selectedContainer = %d, want %d", m.selectedContainer, tc.want)
			}
		})
	}
}

// A full page is pgdown's body scroll, ctrl+f included, so the keybar's
// page hints stay true.
func TestFullPageMotionScrollsTheBody(t *testing.T) {
	m := goldenPodDetailModel(t, 120, 24)
	m.pod.ContainerInfos = make([]kube.ContainerInfo, 12)
	if !m.bodyOverflows() {
		t.Fatal("fixture body does not overflow")
	}
	m = step(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if m.bodyOffset == 0 || m.selectedContainer != 0 {
		t.Fatalf("ctrl+f: bodyOffset=%d selectedContainer=%d, want a scrolled body and an unmoved cursor", m.bodyOffset, m.selectedContainer)
	}
	m = step(t, m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.bodyOffset != 0 {
		t.Fatalf("ctrl+b left bodyOffset=%d, want 0", m.bodyOffset)
	}
}
