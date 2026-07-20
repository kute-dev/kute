package timeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenNow is a fixed reference instant — not time.Now() — that every feed
// row's timestamp is built from. Unlike every other screen's rows (9b's
// events, 5a's EVENTS grid), timeline's renderRow (view.go) prints e.Time
// via an ABSOLUTE local-clock format ("15:04:05"), not a relative "Nm ago"
// bucket, so a time.Now()-relative offset would print a different
// wall-clock string on every separate `go test` invocation — the plain
// AGE-fixture trick documented on browse's goldenPod/poddetail's
// goldenCrashLoopPod doesn't apply to this one column. Pinning to a fixed
// instant, and forcing the model's window to "all time" once loaded (see
// loadFixedFeed below, since Now()-relative window cutoffs would eventually
// exclude any fixed past instant), is what keeps the feed rows' printed
// text byte-identical no matter when the suite runs.
var goldenNow = time.Date(2025, 6, 10, 14, 30, 0, 0, time.Local)

// loadFixedFeed feeds m a hand-built loadedMsg directly (bypassing the real
// m.load()'s Events/Lister round trip entirely — golden fixtures don't need
// a live cluster, just deterministic TimelineEntry values) and forces the
// window to "all time" before the load lands, so recomputeVisible's
// Now()-relative cutoff never filters out goldenFixedNow's fixed-past
// entries. The header/strip then read "all time" rather than the usual
// default "last 30m" tag — an honest reflection of the window being
// deliberately bypassed for this fixture, not a bug.
func loadFixedFeed(m Model, entries, rail []kube.TimelineEntry, railDeployment string) Model {
	m.window = 0
	updated, _ := m.Update(loadedMsg{entries: entries, rail: rail, railDeployment: railDeployment})
	return *updated.(*Model)
}

// goldenNamespaceModel builds a deterministic §16a namespace-scoped screen:
// a merged feed mixing one rollout entry (⇅ purple, "the visual anchor" per
// docs/design README.md §16a), two container restarts, and three Events
// (two Warning, one Normal) — six entries, newest-first. 16a never grows a
// revision rail (rolloutsForScope only ever returns one for an object-scoped
// screen), matching timeline_test.go's own
// TestNamespaceScopedLoadMergesEventsAndRestarts assertion.
func goldenNamespaceModel(width, height int) Model {
	entries := []kube.TimelineEntry{
		{Time: goldenNow.Add(-90 * time.Second), Kind: kube.TimelineEvent, Object: "Pod/api-gateway-9x2kd", Namespace: "default", Severity: "Warning", Reason: "BackOff", Message: "back-off restarting failed container"},
		{Time: goldenNow.Add(-3 * time.Minute), Kind: kube.TimelineRestart, Object: "Pod/api-gateway-9x2kd", Namespace: "default", Reason: "Restarted", Message: "app · OOMKilled · exit 137"},
		{Time: goldenNow.Add(-6 * time.Minute), Kind: kube.TimelineRollout, Object: "Deployment/api-gateway", Namespace: "default", Reason: "Rollout", Message: "revision 5 · api-gateway:2.3.1", Revision: 5, Image: "api-gateway:2.3.1"},
		{Time: goldenNow.Add(-11 * time.Minute), Kind: kube.TimelineEvent, Object: "Pod/worker-77f9c-abcde", Namespace: "default", Severity: "Warning", Reason: "FailedScheduling", Message: "0/3 nodes are available: insufficient cpu"},
		{Time: goldenNow.Add(-18 * time.Minute), Kind: kube.TimelineRestart, Object: "Pod/worker-77f9c-abcde", Namespace: "default", Reason: "Restarted", Message: "app · OOMKilled · exit 137"},
		{Time: goldenNow.Add(-24 * time.Minute), Kind: kube.TimelineEvent, Object: "Pod/api-gateway-9x2kd", Namespace: "default", Severity: "Normal", Reason: "Pulled", Message: `Successfully pulled image "api-gateway:2.3.1"`},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{}, Namespace: "default"})
	m.SetSize(width, height)
	return loadFixedFeed(m, entries, nil, "")
}

// goldenObjectModel builds a deterministic §16b object-scoped screen: a Pod
// owned (via its ReplicaSet) by Deployment "checkout-api", with a merged
// feed of its own restart, two Events, and its rollout entry, plus a
// 3-revision rail (revision 5 current, highlighted). The rail's own entries
// deliberately use time.Now()-relative offsets rather than goldenNow: rail
// rows render via shortAge's relative "Nm ago"/"Nh ago" text (view.go's
// railLines), which — like every other screen's AGE column — stays stable
// because both the offset and the shortAge computation it feeds are
// (re)computed together, microseconds apart, on every test run; pinning
// them to goldenNow instead would make the rail's "ago" text drift by a
// calendar day every day the fixture ages, which is the opposite of what
// browse/poddetail's own goldenPod comments call out for that pattern.
func goldenObjectModel(width, height int) Model {
	feed := []kube.TimelineEntry{
		{Time: goldenNow.Add(-90 * time.Second), Kind: kube.TimelineEvent, Object: "Pod/checkout-api-7d9f6c8b95-k2m9x", Namespace: "default", Severity: "Warning", Reason: "BackOff", Message: "back-off restarting failed container"},
		{Time: goldenNow.Add(-2 * time.Minute), Kind: kube.TimelineRollout, Object: "Deployment/checkout-api", Namespace: "default", Reason: "Rollout", Message: "revision 5 · checkout-api:1.9.0", Revision: 5, Image: "checkout-api:1.9.0"},
		{Time: goldenNow.Add(-4 * time.Minute), Kind: kube.TimelineRestart, Object: "Pod/checkout-api-7d9f6c8b95-k2m9x", Namespace: "default", Reason: "Restarted", Message: "app · OOMKilled · exit 137"},
		{Time: goldenNow.Add(-5 * time.Minute), Kind: kube.TimelineEvent, Object: "Pod/checkout-api-7d9f6c8b95-k2m9x", Namespace: "default", Severity: "Normal", Reason: "Started", Message: "Started container checkout-api"},
	}
	rail := []kube.TimelineEntry{
		{Time: time.Now().Add(-2 * time.Minute), Kind: kube.TimelineRollout, Object: "Deployment/checkout-api", Namespace: "default", Reason: "Rollout", Message: "revision 5 · checkout-api:1.9.0", Revision: 5, Image: "checkout-api:1.9.0"},
		{Time: time.Now().Add(-45 * time.Minute), Kind: kube.TimelineRollout, Object: "Deployment/checkout-api", Namespace: "default", Reason: "Rollout", Message: "revision 4 · checkout-api:1.8.2", Revision: 4, Image: "checkout-api:1.8.2"},
		{Time: time.Now().Add(-3 * time.Hour), Kind: kube.TimelineRollout, Object: "Deployment/checkout-api", Namespace: "default", Reason: "Rollout", Message: "revision 3 · checkout-api:1.8.0", Revision: 3, Image: "checkout-api:1.8.0"},
	}
	m := New(Config{
		Session: newSession(), Events: fakeEvents{}, Namespace: "default",
		ObjectKind: kube.KindPod, ObjectName: "checkout-api-7d9f6c8b95-k2m9x",
	})
	m.SetSize(width, height)
	return loadFixedFeed(m, feed, rail, "checkout-api")
}

func goldenFixtures() map[string]string {
	return map[string]string{
		"namespace-120x36.golden": goldenNamespaceModel(120, 36).Render(),
		"namespace-80x24.golden":  goldenNamespaceModel(80, 24).Render(),
		"object-120x36.golden":    goldenObjectModel(120, 36).Render(),
		"object-80x24.golden":     goldenObjectModel(80, 24).Render(),
	}
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate timeline golden fixtures")
	}
	dir := filepath.Join("..", "..", "..", "..", "test", "golden", "timeline")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for name, got := range goldenFixtures() {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenFixtures() {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "..", "test", "golden", "timeline", name)
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

// truecolorGoldenFixtures pins the object-scoped §16b screen's Theme-token-
// to-cell color mapping in both themes — the state with the revision rail,
// the package's most color-sensitive surface (rollout's purple ⇅, the
// current-revision highlight, restart/warning hues) — same pattern as
// setup/browse/podlogs' own truecolorGoldenFixtures. The profile swap is
// global, so this package must not run these in parallel with other renders
// (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	loc := tui.Location{Context: "microk8s-cluster", Namespace: "default"}
	dark := goldenObjectModel(120, 36)
	dark.session = &tui.Session{Location: loc, Theme: tui.Dark()}
	light := goldenObjectModel(120, 36)
	light.session = &tui.Session{Location: loc, Theme: tui.Light()}
	return map[string]string{
		"object-120x36-dark.golden":  dark.Render(),
		"object-120x36-light.golden": light.Render(),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate timeline golden fixtures")
	}
	dir := filepath.Join("..", "..", "..", "..", "test", "golden", "timeline")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for name, got := range truecolorGoldenFixtures(t) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenFixtures(t *testing.T) {
	for name, got := range truecolorGoldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "..", "test", "golden", "timeline", name)
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
