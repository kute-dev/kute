package cronjobdetail

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenDetailNow is a fixed instant so every relative age/duration cell —
// facts grid LAST SCHEDULE, the failure band's "N ago", and the Jobs
// table's own START/DURATION columns — renders deterministically across
// runs (mirrors browse/golden_test.go's goldenPod comment on the same
// hazard).
var goldenDetailNow = time.Date(2026, 3, 3, 9, 15, 0, 0, time.UTC)

// goldenDetailFailureModel builds §36e's own "failure/history" golden: a
// CronJob with a retained failed latest run (Job condition plus a collected
// Pod's own exit code, §3.5), an older succeeded run, and a currently active
// run — exercising the failure band, a populated facts grid (ACTIVE>0), and
// a multi-row Jobs table in one screenshot.
func goldenDetailFailureModel(t *testing.T, width, height int) Model {
	t.Helper()
	c, cj := newFakeCronJob("default", "nightly")
	cj.Spec.ConcurrencyPolicy = "Forbid"
	cj.Spec.StartingDeadlineSeconds = int64Ptr(600)
	cj.Spec.JobTemplate.Spec.BackoffLimit = int32Ptr(4)

	older := succeededJob("default", "nightly-29310500", cj, goldenDetailNow.Add(-26*time.Hour))
	failed := failedJob("default", "nightly-29310600", cj, goldenDetailNow.Add(-40*time.Minute))
	active := activeJob("default", "nightly-29310650", cj, goldenDetailNow.Add(-2*time.Minute))
	failedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nightly-29310600-x8z2p", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{controllerRef("Job", failed.Name, failed.UID)},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}},
			}},
		},
	}
	c.Seed(kube.KindJob, older, failed, active)
	c.Seed(kube.KindPod, failedPod)

	m := newModel(t, c, "default", "nightly")
	m.SetSize(width, height)
	m.now = goldenDetailNow
	return m
}

// goldenDetailNoHistoryModel builds the "no-history" golden: a CronJob with
// no retained Jobs at all — the facts grid and empty Jobs table render, but
// no failure band (§36e task 15: "no retained Jobs [is] a valid detail
// state ... not a whole-screen empty state").
func goldenDetailNoHistoryModel(t *testing.T, width, height int) Model {
	t.Helper()
	c, cj := newFakeCronJob("default", "quiet")
	cj.Spec.Schedule = "0 4 * * 0"
	m := newModel(t, c, "default", "quiet")
	m.SetSize(width, height)
	m.now = goldenDetailNow
	return m
}

func goldenDetailFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"failure-120x40.golden":    goldentest.Plain(goldenDetailFailureModel(t, 120, 40).Render()),
		"failure-80x24.golden":     goldentest.Plain(goldenDetailFailureModel(t, 80, 24).Render()),
		"no-history-120x40.golden": goldentest.Plain(goldenDetailNoHistoryModel(t, 120, 40).Render()),
		"no-history-80x24.golden":  goldentest.Plain(goldenDetailNoHistoryModel(t, 80, 24).Render()),
	}
}

func goldenDetailDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "cronjobdetail")
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate cronjobdetail golden fixtures")
	}
	if err := os.MkdirAll(goldenDetailDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenDetailFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDetailDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenDetailFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenDetailDir(), name)
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

// truecolorDetailGoldenFixtures pins the failure-band/facts-grid/glyph color
// mapping in both themes — the ErrBanner-token failure band, the failed
// Job's red glyph/OUTCOME cell, the active run's warm glyph — a
// profile-less golden can't see (mirrors browse/golden_test.go's own
// truecolorGoldenFixtures). The profile swap is global, so this file's
// tests must never run t.Parallel (none do).
func truecolorDetailGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	dark := goldenDetailFailureModel(t, 120, 40)
	light := goldenDetailFailureModel(t, 120, 40)
	light.session.Theme = tui.Light()
	return map[string]string{
		"failure-120x40-dark.golden":  goldentest.Truecolor(dark.Render()),
		"failure-120x40-light.golden": goldentest.Truecolor(light.Render()),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate cronjobdetail golden fixtures")
	}
	if err := os.MkdirAll(goldenDetailDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range truecolorDetailGoldenFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDetailDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenFixtures(t *testing.T) {
	for name, got := range truecolorDetailGoldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenDetailDir(), name)
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
