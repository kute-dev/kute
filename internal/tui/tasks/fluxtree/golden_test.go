package fluxtree

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube/fake"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenModel renders §30b against the demo cluster's own Flux fixtures —
// the same objects `--demo` shows, so a golden and a screenshot can't drift
// apart. Every age on this screen is relative to time.Now() (the fixtures
// build their timestamps that way and both the REVISION and RECONCILED
// cells are time.Since reads), which is what keeps the rendered text stable
// as the fixture ages.
func goldenModel(t *testing.T, expanded bool, width, height int) *Model {
	t.Helper()
	c := fake.NewDemo()
	reg, groups := resources.BuildDiscoveredRegistry(c.DiscoveredKinds(), c)
	sess := &tui.Session{
		Theme: tui.Dark(), Registry: reg, Groups: groups,
		Location: tui.Location{Context: "microk8s-cluster", Namespace: "flux-system"},
	}
	m := New(Config{Session: sess, Lister: c, Mutator: c})
	m.SetSize(width, height)
	upd, _ := m.Update(m.load()())
	got := upd.(*Model)
	if expanded {
		next, _ := got.Update(tea.KeyPressMsg{Text: "tab"})
		got = next.(*Model)
	}
	return got
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "fluxtree")
}

func goldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"120x36.golden":          goldentest.Plain(goldenModel(t, false, 120, 36).Render()),
		"80x24.golden":           goldentest.Plain(goldenModel(t, false, 80, 24).Render()),
		"expanded-120x36.golden": goldentest.Plain(goldenModel(t, true, 120, 36).Render()),
	}
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate fluxtree golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenDir(), name)
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

// truecolorGoldenFixtures pin what the plain fixtures can't see, and what
// this screen is read for: the status hue of every row in a chain at once —
// a failing reconciler under a healthy source, and §30a's amber suspended
// beside it. The profile swap is global, so these must not run parallel
// with other renders in this package (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	dark := goldenModel(t, false, 120, 36)
	light := goldenModel(t, false, 120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"120x36-dark.golden":  goldentest.Truecolor(dark.Render()),
		"120x36-light.golden": goldentest.Truecolor(light.Render()),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate fluxtree golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range truecolorGoldenFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenFixtures(t *testing.T) {
	for name, got := range truecolorGoldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenDir(), name)
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
