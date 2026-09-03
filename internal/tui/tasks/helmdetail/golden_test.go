package helmdetail

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

func goldenModel(t *testing.T, width, height int, theme tui.Theme) Model {
	t.Helper()
	release := pendingRelease()
	lister := &recordingLister{objects: map[kube.ResourceKind][]runtime.Object{
		kube.KindHelmRelease: {kube.NewHelmReleaseObject(release)},
	}}
	events := fakeEvents{{Type: "Normal", Reason: "Completed", Message: "Job completed", Object: "Job/timesheet-backend-migrate", Namespace: "timesheet", LastSeen: release.Hooks[0].LastRun.StartedAt.Add(74 * time.Minute)}}
	sess := diagnosticSession()
	sess.Theme = theme
	m := New(Config{Session: sess, Lister: lister, Events: events, Release: release})
	m.SetSize(width, height)
	updated, _ := m.Update(m.load()())
	m = *updated.(*Model)
	m.now = release.Updated.Add(90 * time.Minute)
	m.conn = kube.ConnState{Phase: kube.ConnConnected, Latency: 12 * time.Millisecond}
	return m
}

func goldenFixtures(t *testing.T) map[string]string {
	return map[string]string{
		"120x36.golden":       goldentest.Plain(goldenModel(t, 120, 36, tui.Dark()).Render()),
		"80x24.golden":        goldentest.Plain(goldenModel(t, 80, 24, tui.Dark()).Render()),
		"120x36-dark.golden":  goldentest.Truecolor(goldenModel(t, 120, 36, tui.Dark()).Render()),
		"120x36-light.golden": goldentest.Truecolor(goldenModel(t, 120, 36, tui.Light()).Render()),
	}
}

func goldenDir() string { return filepath.Join("..", "..", "..", "..", "test", "golden", "helmdetail") }

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, got := range goldenFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenFixtures(t) {
		want, err := os.ReadFile(filepath.Join(goldenDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != got {
			t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, want, got)
		}
	}
}
