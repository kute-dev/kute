package overview

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

func plain(s string) string { return ansi.Strip(s) }

func newSession() *tui.Session {
	return &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "test-cluster"}, Registry: resources.DefaultRegistry()}
}

// fakeLister is a minimal resources.RawLister keyed by kind — every fixture
// case populates only the kinds it needs, mirroring browse's own
// fakeLister test helper.
type fakeLister struct {
	objects map[kube.ResourceKind][]runtime.Object
}

func (f *fakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	return f.objects[kind], nil
}

type fakeNodeMetrics struct {
	metrics map[string]kube.NodeMetric
}

func (f *fakeNodeMetrics) NodeMetrics(_ context.Context) (map[string]kube.NodeMetric, error) {
	return f.metrics, nil
}

func testNode(name string, ready, cordoned bool, cpuMilli, memBytes, pods int64) *corev1.Node {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(memBytes, resource.BinarySI),
				corev1.ResourcePods:   *resource.NewQuantity(pods, resource.DecimalSI),
			},
			NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.30.2"},
		},
	}
}

func testPod(ns, name string, phase corev1.PodPhase) *corev1.Pod {
	// projectPod (internal/resources/projections.go) only reads StatusOK for
	// a Running pod when every container's ContainerStatuses entry is
	// Ready:true — an empty ContainerStatuses (0/1 ready) projects as Warn
	// regardless of Phase, so a "healthy" fixture needs one to actually
	// read as OK.
	var statuses []corev1.ContainerStatus
	if phase == corev1.PodRunning {
		statuses = []corev1.ContainerStatus{{Name: "app", Ready: true}}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status:     corev1.PodStatus{Phase: phase, ContainerStatuses: statuses},
	}
}

func testReplicaSet(ns, deployment string, revision int, age time.Duration) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         ns,
			Name:              deployment + "-abc123",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-age)),
			OwnerReferences:   []metav1.OwnerReference{{Kind: "Deployment", Name: deployment}},
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": itoa(revision)},
		},
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func baseLister() *fakeLister {
	return &fakeLister{objects: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {
			testNode("node-a", true, false, 4000, 16*1024*1024*1024, 110),
			testNode("node-b", false, false, 4000, 16*1024*1024*1024, 110),
			testNode("node-c", true, true, 4000, 16*1024*1024*1024, 110),
		},
		kube.KindPod: {
			testPod("ns1", "web-1", corev1.PodRunning),
			testPod("ns1", "worker-1", corev1.PodFailed),
			testPod("ns2", "cache-1", corev1.PodPending),
		},
		kube.KindNamespace: {
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns2"}},
		},
		kube.KindReplicaSet: {
			testReplicaSet("ns1", "web", 3, 5*time.Minute),
			testReplicaSet("ns1", "old-app", 7, 2*time.Hour),
		},
	}}
}

// step drains a tea.BatchMsg fan-out synchronously (mirrors whocan/timeline's
// own test helper of the same name).
func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				m = step(t, m, c())
			}
		}
		return m
	}
	updated, cmd := m.Update(msg)
	next := *updated.(*Model)
	if cmd != nil {
		return step(t, next, cmd())
	}
	return next
}

func TestLoadAggregatesNodesPodsAndRecentChanges(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: baseLister(), NodeMetrics: &fakeNodeMetrics{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready", m.state)
	}
	if m.nodeCount != 3 || m.podCount != 3 || m.nsCount != 2 {
		t.Fatalf("counts = (nodes=%d pods=%d ns=%d), want (3,3,2)", m.nodeCount, m.podCount, m.nsCount)
	}
	if len(m.nodeTrouble) != 2 {
		t.Fatalf("nodeTrouble = %d, want 2 (NotReady node-b + cordoned node-c)", len(m.nodeTrouble))
	}
	if len(m.podTrouble) != 2 {
		t.Fatalf("podTrouble = %d, want 2 (Failed worker-1 + Pending cache-1)", len(m.podTrouble))
	}
	// Only the 5m-old ReplicaSet falls inside the fixed 30m window — the
	// 2h-old one must not appear in RECENT CHANGES.
	if len(m.changes) != 1 || m.changes[0].Object != "Deployment/web" {
		t.Fatalf("changes = %+v, want exactly the recent Deployment/web rollout", m.changes)
	}
	// The fake NodeMetrics reader returns an empty map here — no
	// metrics-server poll ever landed, so CAPACITY's cpu/mem bars must be
	// omitted rather than showing a flatlined 0-used bar.
	if m.metricsAvailable {
		t.Fatalf("metricsAvailable = true, want false with no metrics polled")
	}

	view := plain(m.Render())
	if !strings.Contains(view, "Cluster Overview") || !strings.Contains(view, "cluster-scoped") {
		t.Fatalf("expected cluster-scoped breadcrumb in view:\n%s", view)
	}
	if !strings.Contains(view, "no metrics-server installed") {
		t.Fatalf("expected the no-metrics-server note in view:\n%s", view)
	}
	if !strings.Contains(view, "node-b") || !strings.Contains(view, "node-c") {
		t.Fatalf("expected both trouble nodes in view:\n%s", view)
	}
	if !strings.Contains(view, "worker-1") || !strings.Contains(view, "cache-1") {
		t.Fatalf("expected both trouble pods in view:\n%s", view)
	}
}

func TestNMetricsSentinelDoesNotCountAsAvailable(t *testing.T) {
	// kube.NodeMetric{CPU:"n/a"} is the fake cluster's own no-metrics-server
	// sentinel (kube/fake.Cluster.NodeMetrics) — a non-empty map full of
	// sentinels must still read as "no metrics", not a real (zero-usage)
	// reading.
	metrics := map[string]kube.NodeMetric{
		"node-a": {CPU: "n/a", MEM: "n/a"},
		"node-c": {CPU: "n/a", MEM: "n/a"},
	}
	m := New(Config{Session: newSession(), Lister: baseLister(), NodeMetrics: &fakeNodeMetrics{metrics: metrics}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.metricsAvailable {
		t.Fatalf("metricsAvailable = true, want false when every node reports the n/a sentinel")
	}
}

func TestAllHealthyRendersGreenAllClear(t *testing.T) {
	lister := &fakeLister{objects: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a", true, false, 4000, 16*1024*1024*1024, 110)},
		kube.KindPod:  {testPod("ns1", "web-1", corev1.PodRunning)},
	}}
	m := New(Config{Session: newSession(), Lister: lister, NodeMetrics: &fakeNodeMetrics{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if len(m.nodeTrouble) != 0 || len(m.podTrouble) != 0 {
		t.Fatalf("expected no trouble, got nodes=%v pods=%v", m.nodeTrouble, m.podTrouble)
	}
	view := plain(m.Render())
	if !strings.Contains(view, "nothing unhealthy") {
		t.Fatalf("expected the strip's all-clear line in view:\n%s", view)
	}
	if !strings.Contains(view, "nodes ready") {
		t.Fatalf("expected NODES' all-clear line in view:\n%s", view)
	}
	if !strings.Contains(view, "no changes in the last 30m") {
		t.Fatalf("expected RECENT CHANGES' empty line in view:\n%s", view)
	}
}

func TestTabCyclesFocusAndEnterOpensNodeDetail(t *testing.T) {
	var openedNode string
	m := New(Config{
		Session:     newSession(),
		Lister:      baseLister(),
		NodeMetrics: &fakeNodeMetrics{},
		OpenNodeDetail: func(nodeName string, w, h int) (tea.Model, tea.Cmd) {
			openedNode = nodeName
			return &fakeTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.focus != panelNodes {
		t.Fatalf("initial focus = %v, want panelNodes", m.focus)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	if _, ok := updated.(*fakeTask); !ok {
		t.Fatalf("expected Enter on NODES to push the node-detail task, got %T", updated)
	}
	// sortTrouble ranks Fail before cordoned-only Neutral, so the first
	// selectable row is node-b (NotReady), not node-c (merely cordoned).
	if openedNode != "node-b" {
		t.Fatalf("openNodeDetail called with %q, want node-b", openedNode)
	}

	m.nextPanel()
	if m.focus != panelTrouble {
		t.Fatalf("focus after one tab = %v, want panelTrouble", m.focus)
	}
	m.nextPanel()
	if m.focus != panelChanges {
		t.Fatalf("focus after two tabs = %v, want panelChanges", m.focus)
	}
}

func TestEnterOnTroublePodBacksAndJumps(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: baseLister(), NodeMetrics: &fakeNodeMetrics{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m.focus = panelTrouble
	_, cmd := m.Update(tea.KeyPressMsg{Text: "enter"})
	if cmd == nil {
		t.Fatalf("expected Enter on TROUBLE to return a command")
	}
	// tea.Sequence's result is an unexported slice-of-Cmd type — reflect
	// over it rather than naming it, so this test doesn't depend on
	// bubbletea internals.
	v := reflect.ValueOf(cmd())
	if v.Kind() != reflect.Slice {
		t.Fatalf("expected a sequence of commands, got %T", cmd())
	}
	var sawBack, sawGoto bool
	var gotoMsg tui.GotoResourceMsg
	for i := range v.Len() {
		sub, ok := v.Index(i).Interface().(tea.Cmd)
		if !ok || sub == nil {
			continue
		}
		switch out := sub().(type) {
		case tui.BackMsg:
			sawBack = true
		case tui.GotoResourceMsg:
			sawGoto = true
			gotoMsg = out
		}
	}
	if !sawBack || !sawGoto {
		t.Fatalf("expected both BackMsg and GotoResourceMsg, got back=%v goto=%v", sawBack, sawGoto)
	}
	if gotoMsg.Kind != kube.KindPod || gotoMsg.Name == "" {
		t.Fatalf("GotoResourceMsg = %+v, want a Pod jump", gotoMsg)
	}
}

func TestRendersInBothThemes(t *testing.T) {
	for _, theme := range []tui.Theme{tui.Dark(), tui.Light()} {
		sess := newSession()
		sess.Theme = theme
		m := New(Config{Session: sess, Lister: baseLister(), NodeMetrics: &fakeNodeMetrics{}})
		m.SetSize(120, 36)
		m = step(t, m, m.Init()())
		view := plain(m.Render())
		if !strings.Contains(view, "OVERVIEW") || !strings.Contains(view, "CAPACITY") {
			t.Fatalf("theme render missing expected content:\n%s", view)
		}
	}
}

// fakeTask is a minimal tea.Model stand-in for asserting Update pushed a new
// task (mirrors whocan/poddetail's own such-and-such push tests).
type fakeTask struct{}

func (f *fakeTask) Init() tea.Cmd                       { return nil }
func (f *fakeTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return f, nil }
func (f *fakeTask) View() tea.View                      { return tea.NewView("") }
