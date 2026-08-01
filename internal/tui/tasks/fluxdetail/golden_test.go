package fluxdetail

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kute-dev/kute/internal/kube/fake"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenFixedNow pins the one clock the screen renders from. §31a's failure
// card is the app's only countdown surface ("RECONCILE FAILED · 4m ago ·
// retry in 3m 12s"), and both halves of that line are time arithmetic: the
// age against the Ready condition's own lastTransitionTime, the countdown
// against spec.interval added to it. The demo fixtures build those stamps
// relative to the real time.Now(), which is right for the app and useless
// for a fixture — so the golden model overwrites both resolved instants
// along with m.now, and the line lands on the exact values §31a's own
// mockup quotes.
var goldenFixedNow = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// goldenFluxDetailModel builds §31a against the demo cluster's own Flux
// fixtures — the same seeded objects `--demo` shows, so a golden and a
// screenshot can't drift apart. name selects which band mix renders:
// "nebula-workers" is the failing reconciler (failure card + drill-through
// + inventory fold), "nebula-infra" the suspended one (the amber card and
// the drift note instead).
func goldenFluxDetailModel(t *testing.T, name string, width, height int) *Model {
	t.Helper()
	c := fake.NewDemo()
	reg, groups := resources.BuildDiscoveredRegistry(c.DiscoveredKinds(), c)
	sess := &tui.Session{
		Theme: tui.Dark(), Registry: reg, Groups: groups,
		Location: tui.Location{Context: "microk8s-cluster", Namespace: "flux-system"},
	}
	m := New(Config{
		Session: sess, Lister: c, Mutator: c,
		Kind: "Kustomization", Namespace: "flux-system", Name: name,
	})
	m.SetSize(width, height)
	updated, _ := m.Update(m.load()())
	got := updated.(*Model)
	got.now = goldenFixedNow
	if got.fail != nil {
		got.fail.Since = goldenFixedNow.Add(-4 * time.Minute)
		got.fail.RetryAt = goldenFixedNow.Add(3*time.Minute + 12*time.Second)
	}
	return got
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "fluxdetail")
}

func goldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"failed-120x36.golden":    goldentest.Plain(goldenFluxDetailModel(t, "nebula-workers", 120, 36).Render()),
		"failed-80x24.golden":     goldentest.Plain(goldenFluxDetailModel(t, "nebula-workers", 80, 24).Render()),
		"suspended-120x36.golden": goldentest.Plain(goldenFluxDetailModel(t, "nebula-infra", 120, 36).Render()),
	}
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate fluxdetail golden fixtures")
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

// truecolorGoldenFixtures pin what §31a is actually for. Every band on this
// screen is a colour claim the plain fixtures render colourless: the failure
// card's red tint against the *amber* suspended card (a paused reconciler is
// not a broken one — §30a's rule carried into detail), and the inventory's
// per-object status glyphs. Both cards are pinned, in both themes.
//
// The profile swap is global, so these must not run parallel with other
// renders in this package (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	dark := goldenFluxDetailModel(t, "nebula-workers", 120, 36)
	light := goldenFluxDetailModel(t, "nebula-workers", 120, 36)
	light.session.Theme = tui.Light()
	suspendedDark := goldenFluxDetailModel(t, "nebula-infra", 120, 36)
	suspendedLight := goldenFluxDetailModel(t, "nebula-infra", 120, 36)
	suspendedLight.session.Theme = tui.Light()
	return map[string]string{
		"failed-120x36-dark.golden":     goldentest.Truecolor(dark.Render()),
		"failed-120x36-light.golden":    goldentest.Truecolor(light.Render()),
		"suspended-120x36-dark.golden":  goldentest.Truecolor(suspendedDark.Render()),
		"suspended-120x36-light.golden": goldentest.Truecolor(suspendedLight.Render()),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate fluxdetail golden fixtures")
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
