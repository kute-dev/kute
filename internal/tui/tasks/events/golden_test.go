package events

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

// fakeLister is the events package's own RawLister test double — 9b's
// best-effort "actively-failing object" red/yellow cross-check (load.go's
// failingPods) needs a real lister backing a real resources.Registry, which
// events_test.go's unit tests never wire (they leave Lister nil, so the
// cross-check degrades to "every warning renders yellow").
type fakeLister struct {
	objs map[kube.ResourceKind][]runtime.Object
}

func (f fakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	all := f.objs[kind]
	if namespace == "" {
		return all, nil
	}
	var out []runtime.Object
	for _, o := range all {
		if p, ok := o.(*corev1.Pod); ok && p.Namespace == namespace {
			out = append(out, o)
		}
	}
	return out, nil
}

// goldenCrashLoopPod is the pod BackOff/OOMKilled events below are tied to —
// CrashLoopBackOff maps to resources.StatusFail (projectPod), which is what
// makes load.go's failingPods mark it, which is what turns its Warning
// group's glyph/text red per docs/design README.md §9b ("red reserved for
// events tied to an actively-failing object").
func goldenCrashLoopPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "nva-worker-9k2ss", Namespace: "nva-stage"},
		Spec:       corev1.PodSpec{NodeName: "worker-01", Containers: []corev1.Container{{Name: "worker"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "worker", Ready: false, RestartCount: 41,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			}},
		},
	}
}

// goldenPendingPod backs the FailedScheduling warning below — Pending maps
// to resources.StatusWarn (not StatusFail), so this pod's warning stays
// yellow instead of red: the other half of 9b's red-vs-yellow rule.
func goldenPendingPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "cache-0", Namespace: "nva-stage"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "cache"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "cache", Ready: false,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
			}},
		},
	}
}

// goldenEvents is one deterministic namespace-scoped 9b screen: a
// deduped ×41 BackOff warning on the crashlooping pod (red), a yellow
// FailedScheduling warning on a merely-pending pod, and four distinct
// Normal reasons that fold into one summary line. All LastSeen values are
// *relative* ages (time.Now().Add(-N)) — a pinned absolute timestamp would
// make the LAST column (and the default 1h window's cutoff) drift by a day
// every day, the same reasoning browse's goldenPod/poddetail's
// goldenCrashLoopPod comments give for their own AGE fixtures.
func goldenEvents() []kube.Event {
	now := time.Now()
	return []kube.Event{
		{
			Type: "Warning", Reason: "BackOff", Object: "Pod/nva-worker-9k2ss", Namespace: "nva-stage",
			Message: "Back-off restarting failed container worker in pod nva-worker-9k2ss_nva-stage(a1b2c3)",
			Count:   41, LastSeen: now.Add(-2 * time.Minute),
		},
		{
			Type: "Warning", Reason: "FailedScheduling", Object: "Pod/cache-0", Namespace: "nva-stage",
			Message: "0/5 nodes are available: 5 Insufficient cpu.",
			Count:   3, LastSeen: now.Add(-8 * time.Minute),
		},
		{
			Type: "Normal", Reason: "Pulled", Object: "Pod/nva-worker-9k2ss", Namespace: "nva-stage",
			Message: "Successfully pulled image \"nva/worker:1.42.0\" in 1.2s",
			Count:   6, LastSeen: now.Add(-30 * time.Minute),
		},
		{
			Type: "Normal", Reason: "Created", Object: "Pod/nva-worker-9k2ss", Namespace: "nva-stage",
			Message: "Created container worker", Count: 6, LastSeen: now.Add(-31 * time.Minute),
		},
		{
			Type: "Normal", Reason: "Started", Object: "Pod/nva-worker-9k2ss", Namespace: "nva-stage",
			Message: "Started container worker", Count: 6, LastSeen: now.Add(-31 * time.Minute),
		},
		{
			Type: "Normal", Reason: "Scheduled", Object: "Pod/cache-0", Namespace: "nva-stage",
			Message: "Successfully assigned nva-stage/cache-0 to worker-01", Count: 1, LastSeen: now.Add(-45 * time.Minute),
		},
	}
}

func goldenEventsModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {goldenCrashLoopPod(), goldenPendingPod()},
	}}
	sess := newSession()
	sess.Location.Namespace = "nva-stage"
	sess.Registry = resources.DefaultRegistry()
	m := New(Config{
		Session:   sess,
		Events:    fakeEvents{namespaceEvents: goldenEvents()},
		Lister:    lister,
		Namespace: "nva-stage",
	})
	m.SetSize(width, height)
	m = step(t, m, m.Init()())
	// The header badge's "· 12ms" comes from the conn-state ping loop — a
	// fixed latency keeps the golden deterministic (mirrors poddetail's own
	// golden model).
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected, Latency: 12 * time.Millisecond})
	return m
}

func goldenEventsFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"120x36.golden": goldenEventsModel(t, 120, 36).Render(),
		"80x24.golden":  goldenEventsModel(t, 80, 24).Render(),
	}
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "events")
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate events golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenEventsFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenEventsFixtures(t) {
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

// truecolorGoldenFixtures renders 9b with a forced truecolor profile in both
// themes, pinning the per-cell color mapping (red BackOff vs. yellow
// FailedScheduling vs. neutral folded-normal, conn badge) that the
// profile-less goldens above can't see. The profile swap is global, so
// these tests must not run parallel with other renders in this package
// (none of them do) — mirrors poddetail/browse's own truecolor goldens.
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	dark := goldenEventsModel(t, 120, 36)
	light := goldenEventsModel(t, 120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"120x36-dark.golden":  dark.Render(),
		"120x36-light.golden": light.Render(),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate events golden fixtures")
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
