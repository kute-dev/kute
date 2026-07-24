package browse

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

// TestGenerateWebsiteAssets captures rendered frames the pinned golden
// fixtures don't cover: the empty-query goto palette (design spec 12a,
// highlighted-first-letter aliases) open over the live table, and the
// all-namespaces triage view (6b: unhealthy-first sort, healthy namespaces
// collapsed) — both dark and light. The palette loop reuses goldenModel's
// fixture shape so the backdrop matches the browse package's own 120x36
// golden screenshots. Output feeds website/gen's ANSI-to-HTML pipeline (see
// website/gen/main.go for the full regeneration commands).
func TestGenerateWebsiteAssets(t *testing.T) {
	if os.Getenv("WEBSITE_ASSETS") != "1" {
		t.Skip("set WEBSITE_ASSETS=1 to regenerate website screenshot fixtures")
	}

	outDir := filepath.Join("..", "..", "..", "..", "website", "gen", "fixtures")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		file  string
		theme tui.Theme
	}{
		{"goto-palette-dark.ansi", tui.Dark()},
		{"goto-palette-light.ansi", tui.Light()},
	} {
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

		sess := &tui.Session{
			Registry: resources.DefaultRegistry(),
			Groups:   resources.DefaultGroups(),
			Location: tui.Location{Context: "microk8s-cluster", Namespace: "default", Kind: kube.KindPod},
			Theme:    tc.theme,
			Lister:   lister,
		}

		m := New(Config{Session: sess, Lister: lister, Metrics: metrics})
		m.SetSize(120, 36)
		m = step(t, m, m.load()())
		m = step(t, m, m.loadMetrics(m.metricsEpoch)())
		m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected})

		root := tui.NewWithSession(&m, sess)
		updated, _ := root.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
		updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
		view := updated.(tui.Model).View().Content

		if err := os.WriteFile(filepath.Join(outDir, tc.file), []byte(view), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		file  string
		theme tui.Theme
	}{
		{"triage-dark.ansi", tui.Dark()},
		{"triage-light.ansi", tui.Light()},
	} {
		lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
			kube.KindPod: {
				triagePod("prod", "worker-0", corev1.PodRunning, false, 6, "CrashLoopBackOff", "node-a"),
				triagePod("prod", "cache-0", corev1.PodPending, false, 0, "ContainerCreating", ""),
				triagePod("prod", "api-7d9f6c8-abcde", corev1.PodRunning, true, 0, "", "node-a"),
				triagePod("staging", "web-6f9c9d-8k2pl", corev1.PodRunning, true, 0, "", "node-b"),
				triagePod("staging", "web-6f9c9d-x4m91", corev1.PodRunning, true, 0, "", "node-b"),
			},
			kube.KindNode: {
				&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
				&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
				&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-c"}},
			},
		}}
		sess := &tui.Session{
			Registry: resources.DefaultRegistry(),
			Groups:   resources.DefaultGroups(),
			Location: tui.Location{Context: "microk8s-cluster", Namespace: "prod", Kind: kube.KindPod},
			Theme:    tc.theme,
			Lister:   lister,
		}

		// No Metrics reader here (unlike the goto-palette capture above):
		// resetAndLoad schedules a recurring metrics-poll tea.Tick whenever
		// metrics are wired, and step() drains commands synchronously with
		// no regard for real time, so the "a" switch-namespace below would
		// recurse into that self-rescheduling tick forever.
		m := New(Config{Session: sess, Lister: lister})
		m.SetSize(120, 36)
		m = step(t, m, m.load()())
		m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected, Latency: 12 * time.Millisecond})
		m = step(t, m, tea.KeyPressMsg{Text: "a"})

		if err := os.WriteFile(filepath.Join(outDir, tc.file), []byte(m.Render()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// triagePod builds a namespaced pod fixture for the all-namespaces triage
// capture — same shape as golden_test.go's goldenPod, but with an explicit
// namespace so the fixture can demonstrate cross-namespace grouping.
func triagePod(ns, name string, phase corev1.PodPhase, ready bool, restarts int32, waitingReason string, node string) *corev1.Pod {
	created := metav1.NewTime(time.Now().Add(-90 * time.Minute))
	state := corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: created}}
	if waitingReason != "" {
		state = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: waitingReason}}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Spec:       corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			Phase:             phase,
			ContainerStatuses: []corev1.ContainerStatus{{Ready: ready, RestartCount: restarts, State: state}},
		},
	}
}
