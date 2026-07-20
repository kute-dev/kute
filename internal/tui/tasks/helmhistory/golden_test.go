package helmhistory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenRevisionSecret mirrors helmhistory_test.go's revisionSecret but adds
// the StatusReason/Updated fields the plain unit tests don't need — 18a's
// STATUS cell carries the failure reason verbatim ("failed · hook timeout")
// and the rail's UPDATED column needs a real timestamp to render "N ago".
// Updated is expressed as an age (time.Since-relative), not a pinned
// wall-clock date, so the golden doesn't drift day to day.
func goldenRevisionSecret(namespace, name, chart, chartVersion, status, statusReason string, revision int, age time.Duration) *corev1.Secret {
	return kube.EncodeHelmReleaseSecret(kube.HelmRelease{
		Namespace: namespace, Name: name,
		Chart: chart, ChartVersion: chartVersion,
		Revision: revision, Status: status, StatusReason: statusReason,
		Updated: time.Now().Add(-age),
	})
}

// goldenHelmHistoryModel builds a deterministic 18a `h` rail: postgresql's
// history in the "production" namespace, current revision (4, deployed)
// first, an earlier failed revision carrying its reason verbatim (§18a:
// "failed carries the reason verbatim: failed · hook timeout"), and two
// superseded revisions further back — the mix of ok/fail/neutral glyphs the
// railBody switch (view.go) covers edge to edge.
func goldenHelmHistoryModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {
			goldenRevisionSecret("production", "postgresql", "postgresql", "12.1.9", "deployed", "", 4, 3*time.Minute),
			goldenRevisionSecret("production", "postgresql", "postgresql", "12.1.8", "failed", "hook timeout", 3, 22*time.Minute),
			goldenRevisionSecret("production", "postgresql", "postgresql", "12.1.8", "superseded", "", 2, 3*time.Hour),
			goldenRevisionSecret("production", "postgresql", "postgresql", "12.1.6", "superseded", "", 1, 26*time.Hour),
		},
	}}
	sess := newSession()
	sess.Location.Namespace = "production"
	m := New(Config{
		Session: sess, Lister: lister, Mutator: &fakeMutator{},
		Namespace: "production", Name: "postgresql",
	})
	m.SetSize(width, height)
	m = step(t, m, m.Init()())
	// The header's connected badge comes from the conn-state ping loop — a
	// fixed latency keeps the golden deterministic (mirrors poddetail's own
	// goldenPodDetailModel).
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected, Latency: 12 * time.Millisecond})
	return m
}

func goldenHelmHistoryFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"120x36.golden": goldenHelmHistoryModel(t, 120, 36).Render(),
		"80x24.golden":  goldenHelmHistoryModel(t, 80, 24).Render(),
	}
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "helmhistory")
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate helmhistory golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenHelmHistoryFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenHelmHistoryFixtures(t) {
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

// truecolorGoldenFixtures renders 18a's history rail with a forced truecolor
// profile in both themes, pinning the per-cell color mapping (status
// glyphs, current-revision highlight, conn badge) the profile-less goldens
// above can't see. The profile swap is global, so these tests must not run
// parallel with other renders in this package (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	dark := goldenHelmHistoryModel(t, 120, 36)
	light := goldenHelmHistoryModel(t, 120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"120x36-dark.golden":  dark.Render(),
		"120x36-light.golden": light.Render(),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate helmhistory golden fixtures")
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
