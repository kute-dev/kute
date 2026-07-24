package browse

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenPod builds a pod fixture with a fixed *age* (creation time relative
// to now) so AGE renders deterministically across runs — a pinned absolute
// date would drift by a day every day, since kube.PodFromObject computes age
// against the wall clock.
func goldenPod(name string, phase corev1.PodPhase, ready bool, restarts int32, waitingReason string, node string) *corev1.Pod {
	created := metav1.NewTime(time.Now().Add(-90 * time.Minute))
	state := corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: created}}
	if waitingReason != "" {
		state = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: waitingReason}}
	} else if phase == corev1.PodSucceeded {
		state = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, Reason: "Completed"}}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", CreationTimestamp: created},
		Spec:       corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			Phase:             phase,
			ContainerStatuses: []corev1.ContainerStatus{{Ready: ready, RestartCount: restarts, State: state}},
		},
	}
}

func goldenModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			goldenPod("api-7d9f6c8-abcde", corev1.PodRunning, true, 0, "", "node-a"),
			goldenPod("worker-0", corev1.PodRunning, false, 6, "CrashLoopBackOff", "node-a"),
			goldenPod("cache-0", corev1.PodPending, false, 0, "ContainerCreating", ""),
			goldenPod("migrate-job-x8z2p", corev1.PodSucceeded, true, 0, "", "node-a"),
		},
		kube.KindNode: {
			&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
			&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
			&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-c"}},
		},
	}}
	metrics := fakeMetrics{metrics: map[string]kube.PodMetrics{
		"api-7d9f6c8-abcde": {CPU: "45m", MEM: "128Mi", CPUMilli: 45, MemBytes: 128 * 1024 * 1024},
		"worker-0":          {CPU: "890m", MEM: "612Mi", CPUMilli: 890, MemBytes: 612 * 1024 * 1024},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Metrics: metrics})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	m = step(t, m, m.loadMetrics(m.metricsEpoch)())
	// The 2a header badge's "· 12ms" comes from the conn-state ping loop —
	// fixed latency keeps the golden deterministic.
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected, Latency: 12 * time.Millisecond})
	return m
}

func goldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"120x36.golden": goldentest.Plain(goldenModel(t, 120, 36).Render()),
		"80x24.golden":  goldentest.Plain(goldenModel(t, 80, 24).Render()),
	}
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate browse golden fixtures")
	}
	for name, got := range goldenFixtures(t) {
		path := filepath.Join("..", "..", "..", "..", "test", "golden", "browse", name)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "..", "test", "golden", "browse", name)
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

// truecolorGoldenFixtures renders the 2a screen with a forced truecolor
// profile in both themes, pinning the per-cell color mapping (docs/design
// §2a) that the profile-less goldens above can't see.
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	dark := goldenModel(t, 120, 36)
	light := goldenModel(t, 120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"120x36-dark.golden":  goldentest.Truecolor(dark.Render()),
		"120x36-light.golden": goldentest.Truecolor(light.Render()),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate browse golden fixtures")
	}
	for name, got := range truecolorGoldenFixtures(t) {
		path := filepath.Join("..", "..", "..", "..", "test", "golden", "browse", name)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenFixtures(t *testing.T) {
	for name, got := range truecolorGoldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "..", "test", "golden", "browse", name)
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
