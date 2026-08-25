package podlogs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenModel builds a deterministic 5b screen: two containers (so the
// toolbar's "(tab: sidecar)" hint renders), one line of each severity, and
// a restart boundary — covering every distinct render path a single
// fixture can hit. Lines go in through appendEntry rather than
// buffer.Append so the fixtures pin the windowed layout the live stream
// takes; a buffer filled behind appendEntry's back renders through
// visibleWindow's whole-buffer fallback instead (see layoutValid).
func goldenModel(width, height int) Model {
	model := New(Config{Pod: SelectedPod{
		Context:    "prod-eks",
		Namespace:  "default",
		Name:       "nva-worker-9k2ss",
		Containers: []string{"worker", "metrics-sidecar"},
		Restarts:   6,
	}})
	model.SetSize(width, height)
	model.stream = StreamStreaming
	model.view.Timestamps = true
	model.appendEntry(LogEntry{Container: "worker", Timestamp: "10:23:58", Message: "starting server", Severity: SeverityInfo})
	model.appendEntry(LogEntry{Container: "worker", Timestamp: "10:24:00", Message: "queue depth rising", Severity: SeverityWarn})
	model.appendEntry(LogEntry{Boundary: true, Timestamp: "10:24:02", Message: "container restarted · restart 6"})
	model.appendEntry(LogEntry{Container: "worker", Timestamp: "10:24:05", Message: "panic: nil pointer dereference", Severity: SeverityErr})
	model.appendEntry(LogEntry{Container: "worker", Timestamp: "10:24:06", Message: "back to normal"})
	return model
}

// waitingGoldenModel is the new pre-connect state: the active container
// hasn't started yet, so there's no buffer to render — just the badge/
// strip/body all naming the reason (docs/design README.md §5b's
// "waiting-for-container state").
func waitingGoldenModel(width, height int) Model {
	model := New(Config{Pod: SelectedPod{
		Context:    "prod-eks",
		Namespace:  "default",
		Name:       "nva-worker-9k2ss",
		Containers: []string{"worker", "metrics-sidecar"},
	}})
	model.SetSize(width, height)
	model.stream = StreamWaitingForContainer
	model.waitingReason = "ContainerCreating"
	return model
}

func goldenFixtures() map[string]string {
	return map[string]string{
		"120x36.golden":         goldentest.Plain(goldenModel(120, 36).Render()),
		"80x24.golden":          goldentest.Plain(goldenModel(80, 24).Render()),
		"120x36-waiting.golden": goldentest.Plain(waitingGoldenModel(120, 36).Render()),
		"80x24-waiting.golden":  goldentest.Plain(waitingGoldenModel(80, 24).Render()),
	}
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate pod-logs golden fixtures")
	}
	for name, got := range goldenFixtures() {
		path := filepath.Join("..", "..", "..", "..", "test", "golden", "podlogs", name)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenFixtures() {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "..", "test", "golden", "podlogs", name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, string(want), got)
			}
		})
	}
}

// truecolorGoldenFixtures renders 5b with a forced truecolor profile in
// both themes, pinning the per-cell color mapping (docs/design §5b) that
// the profile-less goldens above can't see — same pattern as browse's 2a
// (browse/golden_test.go). The profile swap is global, so this package
// must not run these in parallel with other renders (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	dark := goldenModel(120, 36)
	dark.session = &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "prod-eks"}}
	light := goldenModel(120, 36)
	light.session = &tui.Session{Theme: tui.Light(), Location: tui.Location{Context: "prod-eks"}}
	waitingDark := waitingGoldenModel(120, 36)
	waitingDark.session = &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "prod-eks"}}
	waitingLight := waitingGoldenModel(120, 36)
	waitingLight.session = &tui.Session{Theme: tui.Light(), Location: tui.Location{Context: "prod-eks"}}
	return map[string]string{
		"120x36-dark.golden":          goldentest.Truecolor(dark.Render()),
		"120x36-light.golden":         goldentest.Truecolor(light.Render()),
		"120x36-waiting-dark.golden":  goldentest.Truecolor(waitingDark.Render()),
		"120x36-waiting-light.golden": goldentest.Truecolor(waitingLight.Render()),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate pod-logs golden fixtures")
	}
	for name, got := range truecolorGoldenFixtures(t) {
		path := filepath.Join("..", "..", "..", "..", "test", "golden", "podlogs", name)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenFixtures(t *testing.T) {
	for name, got := range truecolorGoldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "..", "test", "golden", "podlogs", name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, string(want), got)
			}
		})
	}
}
