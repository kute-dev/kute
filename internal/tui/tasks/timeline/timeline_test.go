package timeline

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

func plain(s string) string { return ansi.Strip(s) }

// fakeEvents is a minimal EventsReader test double, mirroring tasks/events'
// own of the same name.
type fakeEvents struct {
	namespaceEvents []kube.Event
	objectEvents    []kube.Event
	err             error
}

func (f fakeEvents) NamespaceEvents(context.Context, string) ([]kube.Event, error) {
	return f.namespaceEvents, f.err
}

func (f fakeEvents) ObjectEvents(context.Context, string, kube.ResourceKind, string) ([]kube.Event, error) {
	return f.objectEvents, f.err
}

// fakeLister mirrors tasks/poddetail's/nodedetail's own of the same name —
// ignores the namespace argument, same as those.
type fakeLister struct {
	objs map[kube.ResourceKind][]runtime.Object
}

func (f fakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	return f.objs[kind], nil
}

func newSession() *tui.Session {
	return &tui.Session{
		Location: tui.Location{Context: "microk8s-cluster", Namespace: "default"},
		Theme:    tui.Dark(),
	}
}

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

func testPod(name, node string, terminatedAgo time.Duration) *corev1.Pod {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}, Spec: corev1.PodSpec{NodeName: node}}
	if terminatedAgo > 0 {
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: "app",
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(time.Now().Add(-terminatedAgo)),
			}},
		}}
	}
	return pod
}

func TestNamespaceScopedLoadMergesEventsAndRestarts(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "restarting", Count: 1, LastSeen: time.Now()},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {testPod("worker-0", "node-a", 5*time.Minute)},
	}}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Lister: lister, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready", m.state)
	}
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want 2 (1 event + 1 restart)", len(m.rows))
	}
	// Newest-first: the just-loaded event (LastSeen ~now) sorts before the
	// 5-minutes-ago restart.
	if m.rows[0].Kind != kube.TimelineEvent || m.rows[1].Kind != kube.TimelineRestart {
		t.Fatalf("rows not newest-first by kind: %+v", m.rows)
	}
	view := plain(m.Render())
	if !strings.Contains(view, "BackOff") {
		t.Fatalf("expected the event's reason in view:\n%s", view)
	}
	if len(m.rail) != 0 {
		t.Fatalf("expected no revision rail in 16a namespace-scoped mode, got %+v", m.rail)
	}
}

// TestFilterQueryShowsHiddenNotice pins the cross-cutting fix (docs/design
// system-wide interactions: "items never silently disappear"): once '/'
// narrows the merged feed, the strip must say how many entries the query
// itself hid, not just show a bare matched count.
func TestFilterQueryShowsHiddenNotice(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "restarting", Count: 1, LastSeen: time.Now()},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {testPod("worker-0", "node-a", 5*time.Minute)},
	}}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Lister: lister, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "/"})
	if !m.filterActive {
		t.Fatal("expected / to activate the filter")
	}
	for _, r := range "BackOff" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	if len(m.rows) != 1 {
		t.Fatalf("expected the filter to narrow to 1 row, got %d", len(m.rows))
	}
	view := plain(m.Render())
	if !strings.Contains(view, "hidden by filter") {
		t.Fatalf("expected the 'hidden by filter' notice:\n%s", view)
	}
}

func TestObjectScopedPodResolvesRevisionRail(t *testing.T) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nva-worker-abc123", Namespace: "default",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": "4"},
			OwnerReferences:   []metav1.OwnerReference{{Kind: "Deployment", Name: "nva-worker"}},
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: "nginx:1.26"}},
		}}},
	}
	pod := testPod("nva-worker-9k2ss", "node-a", 0)
	pod.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "nva-worker-abc123"}}

	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod:        {pod},
		kube.KindReplicaSet: {rs},
	}}
	m := New(Config{
		Session: newSession(), Events: fakeEvents{}, Lister: lister,
		Namespace: "default", ObjectKind: kube.KindPod, ObjectName: "nva-worker-9k2ss",
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if len(m.rail) != 1 || m.railDeployment != "nva-worker" {
		t.Fatalf("expected a 1-entry rail for nva-worker, got rail=%+v deployment=%q", m.rail, m.railDeployment)
	}
	view := plain(m.Render())
	if !strings.Contains(view, "REVISIONS · nva-worker") || !strings.Contains(view, "nginx:1.26") {
		t.Fatalf("expected the revision rail in view:\n%s", view)
	}
}

func TestObjectScopedNodeFiltersRestartsByNodeName(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			testPod("on-node", "node-a", 2*time.Minute),
			testPod("off-node", "node-b", 2*time.Minute),
		},
	}}
	m := New(Config{
		Session: newSession(), Events: fakeEvents{}, Lister: lister,
		Namespace: "", ObjectKind: kube.KindNode, ObjectName: "node-a",
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if len(m.rows) != 1 || m.rows[0].Object != "Pod/on-node" {
		t.Fatalf("expected only on-node's restart, got %+v", m.rows)
	}
	if len(m.rail) != 0 {
		t.Fatalf("expected no rail for a Node (no Deployment concept), got %+v", m.rail)
	}
}

func TestEmptyStateWhenNothingChanged(t *testing.T) {
	m := New(Config{Session: newSession(), Events: fakeEvents{}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty", m.state)
	}
}

func TestEnterOnRowReturnsNavigationCmd(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "m", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	cmd, ok := m.openSelectedObject()
	if !ok || cmd == nil {
		t.Fatal("expected ↵ on a row to produce a navigation command")
	}
}

func TestTimeWindowFiltersOldEntries(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "Recent", Object: "Pod/a", Message: "m", Count: 1, LastSeen: time.Now()},
		{Type: "Warning", Reason: "Ancient", Object: "Pod/b", Message: "m", Count: 1, LastSeen: time.Now().Add(-48 * time.Hour)},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()()) // default window is 30m

	if len(m.rows) != 1 || m.rows[0].Reason != "Recent" {
		t.Fatalf("expected only the recent event within the default 30m window, got %+v", m.rows)
	}

	// Cycle t: 30m -> 1h -> 6h -> 24h -> all. Starting at 30m, 4 more
	// presses reaches "all".
	for range 4 {
		m = step(t, m, tea.KeyPressMsg{Text: "t"})
	}
	if len(m.rows) != 2 {
		t.Fatalf("expected both events once the window is 'all', got %d", len(m.rows))
	}
}

func TestEscSendsBackMsg(t *testing.T) {
	m := New(Config{Session: newSession(), Events: fakeEvents{}})
	m.SetSize(120, 36)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatal("esc did not send BackMsg")
	}
}
