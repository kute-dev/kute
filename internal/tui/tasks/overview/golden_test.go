package overview

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenPressureNode is testNode (overview_test.go) plus a MemoryPressure
// condition — testNode itself only toggles Ready/cordoned, so 19a's "one
// node under pressure" mock case (docs/design README.md §19a: NODES sorts
// pressure/cordoned first) needs its own builder.
func goldenPressureNode(name string, cpuMilli, memBytes, pods int64) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue},
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(memBytes, resource.BinarySI),
				corev1.ResourcePods:   *resource.NewQuantity(pods, resource.DecimalSI),
			},
			NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.30.2"},
		},
	}
}

// goldenCrashPod builds a CrashLoopBackOff pod — projectPod (internal/
// resources/projections.go) reads the waiting reason off ContainerStatuses,
// not Phase, to earn the StatusFail "✕ CrashLoopBackOff" TROUBLE row.
func goldenCrashPod(ns, name, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", Ready: false, RestartCount: 9,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			}},
		},
	}
}

// goldenLister is 19a's mock cluster: 5 nodes (3 healthy, 1 NotReady, 1
// MemPressure — so NODES isn't trivially all-green) across 4 namespaces with
// 11 pods (9 healthy, 1 CrashLoopBackOff, 1 Pending — so TROUBLE has rows
// too). RECENT CHANGES is deliberately NOT sourced from ReplicaSet fixtures
// here: kute.TimelineFromRollouts' entries carry an absolute
// rs.CreationTimestamp that changeRow renders as a bare "15:04" clock string
// (view.go), not a relative "Xm ago" delta like every other timestamp in
// this package — there's no way to pin that text across days without a
// clock-injection seam view.go doesn't have. goldenOverviewModel instead
// pokes m.changes directly with fixed kube.TimelineEntry values after
// load() completes, the same white-box field access overview_test.go's own
// assertions already use in this package.
func goldenLister() *fakeLister {
	return &fakeLister{objects: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {
			testNode("node-a", true, false, 4000, 16*1024*1024*1024, 110),
			testNode("node-b", true, false, 4000, 16*1024*1024*1024, 110),
			goldenPressureNode("node-c", 4000, 16*1024*1024*1024, 110),
			testNode("node-d", false, false, 4000, 16*1024*1024*1024, 110),
			testNode("node-e", true, false, 4000, 16*1024*1024*1024, 110),
		},
		kube.KindPod: {
			testPod("nva-prod", "api-7f9c6-abc12", corev1.PodRunning),
			testPod("nva-prod", "api-7f9c6-def34", corev1.PodRunning),
			testPod("nva-prod", "worker-0", corev1.PodRunning),
			goldenCrashPod("nva-prod", "worker-1", "node-a"),
			testPod("nva-stage", "web-1", corev1.PodRunning),
			testPod("nva-stage", "web-2", corev1.PodPending),
			testPod("kube-system", "coredns-1", corev1.PodRunning),
			testPod("kube-system", "coredns-2", corev1.PodRunning),
			testPod("kube-system", "kube-proxy-node-a", corev1.PodRunning),
			testPod("observability", "grafana-0", corev1.PodRunning),
			testPod("observability", "prometheus-0", corev1.PodRunning),
		},
		kube.KindNamespace: {
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "nva-prod"}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "nva-stage"}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "observability"}},
		},
	}}
}

// goldenNodeMetrics gives 4 of the 5 nodes a real usage reading (node-d is
// NotReady — no kubelet reporting) so CAPACITY's cpu/mem bars render actual
// fill instead of the "no metrics-server installed" fallback.
func goldenNodeMetrics() *fakeNodeMetrics {
	return &fakeNodeMetrics{metrics: map[string]kube.NodeMetric{
		"node-a": {CPU: "1200m", MEM: "6.1Gi", CPUMilli: 1200, MemBytes: 6553600000},
		"node-b": {CPU: "800m", MEM: "4.4Gi", CPUMilli: 800, MemBytes: 4724464025},
		"node-c": {CPU: "3400m", MEM: "14.8Gi", CPUMilli: 3400, MemBytes: 15891392512},
		"node-e": {CPU: "600m", MEM: "3.2Gi", CPUMilli: 600, MemBytes: 3435973836},
	}}
}

// goldenChanges is RECENT CHANGES' fixed two-row fixture — see goldenLister's
// doc comment for why these are hand-built rather than routed through
// kube.TimelineFromRollouts.
func goldenChanges() []kube.TimelineEntry {
	pinned := time.Date(2024, 3, 12, 14, 32, 0, 0, time.UTC)
	return []kube.TimelineEntry{
		{
			Time: pinned, Kind: kube.TimelineRollout,
			Object: "Deployment/nva-worker", Namespace: "nva-prod",
			Reason: "Rollout", Message: "revision 4 · nva/worker:1.44.0", Revision: 4, Image: "nva/worker:1.44.0",
		},
		{
			Time: pinned.Add(-14 * time.Minute), Kind: kube.TimelineRollout,
			Object: "Deployment/nva-web", Namespace: "nva-stage",
			Reason: "Rollout", Message: "revision 2 · nva/web:2.3.1", Revision: 2, Image: "nva/web:2.3.1",
		},
	}
}

// goldenOpenNodeDetailStub/goldenOpenTimelineStub/goldenOpenEventsStub mirror
// poddetail's own goldenPodDetailModel — no-op stubs so the keybar and
// selectable panels render 19a's full behavior, not a degraded subset.
func goldenOpenNodeDetailStub(string, int, int) (tea.Model, tea.Cmd) { return &fakeTask{}, nil }
func goldenOpenTimelineStub(string, int, int) (tea.Model, tea.Cmd)   { return &fakeTask{}, nil }
func goldenOpenEventsStub(string, int, int) (tea.Model, tea.Cmd)     { return &fakeTask{}, nil }

func goldenOverviewModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := New(Config{
		Session:        newSession(),
		Lister:         goldenLister(),
		NodeMetrics:    goldenNodeMetrics(),
		OpenNodeDetail: goldenOpenNodeDetailStub,
		OpenTimeline:   goldenOpenTimelineStub,
		OpenEvents:     goldenOpenEventsStub,
	})
	m.SetSize(width, height)
	m = step(t, m, m.Init()())
	// The header badge's "· 12ms" comes from the conn-state ping loop — a
	// fixed latency keeps the golden deterministic, same as poddetail/
	// browse's own golden models.
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected, Latency: 12 * time.Millisecond})
	// Pin RECENT CHANGES after load() so its "15:04" clock text is stable
	// regardless of what real time this test runs at — see goldenLister's
	// doc comment.
	m.changes = goldenChanges()
	m.changesSel = clamp(m.changesSel, 0, cappedMax(len(m.changes)))
	return m
}

func goldenOverviewFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"120x36.golden": goldenOverviewModel(t, 120, 36).Render(),
		"80x24.golden":  goldenOverviewModel(t, 80, 24).Render(),
	}
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "overview")
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate overview golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenOverviewFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenOverviewFixtures(t) {
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

// truecolorGoldenFixtures renders 19a with a forced truecolor profile in
// both themes, pinning the per-cell color mapping (CAPACITY bars, NODES/
// TROUBLE glyph tones, RECENT CHANGES rollout glyph) that the profile-less
// goldens above can't see. The profile swap is global, so these tests must
// not run parallel with other renders in this package (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	dark := goldenOverviewModel(t, 120, 36)
	light := goldenOverviewModel(t, 120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"120x36-dark.golden":  dark.Render(),
		"120x36-light.golden": light.Render(),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate overview golden fixtures")
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
