package podlogs

import (
	"testing"

	"github.com/kute-dev/kute/internal/tui"
)

func TestKeybarShowsLogsPillAndCoreGroups(t *testing.T) {
	t.Parallel()

	model := testModel() // app, sidecar
	bar := model.Keybar()
	if bar.Pill != tui.ModeBrowse || bar.PillText != "LOGS" {
		t.Fatalf("pill = %v %q", bar.Pill, bar.PillText)
	}

	found := map[string]bool{}
	for _, group := range bar.Groups {
		for _, hint := range group {
			found[hint.Key] = true
		}
	}
	for _, key := range []string{"esc", "space", "w/e", "/", "s", "tab", "ctrl-y"} {
		if !found[key] {
			t.Fatalf("missing key hint %q in %+v", key, bar.Groups)
		}
	}
}

func TestKeybarOmitsContainerCycleForSingleContainerPod(t *testing.T) {
	t.Parallel()

	model := New(Config{Pod: SelectedPod{Name: "api", Containers: []string{"solo"}}})
	bar := model.Keybar()
	for _, group := range bar.Groups {
		for _, hint := range group {
			if hint.Key == "tab" {
				t.Fatalf("single-container pod should not offer tab cycle: %+v", bar.Groups)
			}
		}
	}
}

func TestKeybarShowsFilterPillWhileFiltering(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.filterActive = true
	bar := model.Keybar()
	if bar.Pill != tui.ModeFilter || bar.PillText != "FILTER" {
		t.Fatalf("pill = %v %q", bar.Pill, bar.PillText)
	}
}
