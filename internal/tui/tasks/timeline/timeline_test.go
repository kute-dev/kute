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

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
)

func plain(s string) string { return ansi.Strip(s) }

// fakeEvents is a minimal EventsReader test double — unlike tasks/events'
// own of the same name (which just returns two independent canned slices),
// this one filters a single event list by involvedObject the way
// kube.Cluster's real ObjectEvents does, so eventsForScope's owned-pods
// fan-out (ObjectEvents for the primary object + NamespaceEvents filtered to
// owned pods) can be exercised realistically.
type fakeEvents struct {
	events []kube.Event
	err    error
}

func (f fakeEvents) NamespaceEvents(context.Context, string) ([]kube.Event, error) {
	return f.events, f.err
}

func (f fakeEvents) ObjectEvents(_ context.Context, _ string, kind kube.ResourceKind, name string) ([]kube.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]kube.Event, 0, len(f.events))
	for _, e := range f.events {
		if e.Object == string(kind)+"/"+name {
			out = append(out, e)
		}
	}
	return out, nil
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
	m := New(Config{Session: newSession(), Events: fakeEvents{events: events}, Lister: lister, Namespace: "default"})
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
	m := New(Config{Session: newSession(), Events: fakeEvents{events: events}, Lister: lister, Namespace: "default"})
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
	if !strings.Contains(view, "ROLLOUT HISTORY") || !strings.Contains(view, "nginx:1.26") {
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
	m := New(Config{Session: newSession(), Events: fakeEvents{events: events}, Namespace: "default"})
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
	m := New(Config{Session: newSession(), Events: fakeEvents{events: events}, Namespace: "default"})
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

// TestEOpensEventsNamespaceScoped covers 16a's 'e': the global Events verb
// must push 9b scoped to the same namespace timeline itself is showing,
// with no object kind/name (this bug: 'e' was wired for every other list/
// detail screen but never for tasks/timeline).
func TestEOpensEventsNamespaceScoped(t *testing.T) {
	var gotKind kube.ResourceKind
	var gotNamespace, gotName string
	m := New(Config{
		Session:   newSession(),
		Events:    fakeEvents{},
		Namespace: "default",
		OpenEvents: func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd) {
			gotKind, gotNamespace, gotName = kind, namespace, name
			return &Model{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "e"})
	if cmd != nil {
		t.Fatal("openEvents returns no cmd in this test double, expected nil")
	}
	if gotNamespace != "default" || gotKind != "" || gotName != "" {
		t.Fatalf("expected namespace-scoped events push (kind=%q name=%q namespace=%q), want kind=\"\" name=\"\" namespace=default", gotKind, gotName, gotNamespace)
	}
}

// TestEOpensEventsObjectScoped covers 16b's 'e': object-scoped timelines
// must push 9b scoped to that same object.
func TestEOpensEventsObjectScoped(t *testing.T) {
	var gotKind kube.ResourceKind
	var gotNamespace, gotName string
	m := New(Config{
		Session:    newSession(),
		Events:     fakeEvents{},
		Namespace:  "default",
		ObjectKind: kube.KindPod,
		ObjectName: "worker-0",
		OpenEvents: func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd) {
			gotKind, gotNamespace, gotName = kind, namespace, name
			return &Model{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m.Update(tea.KeyPressMsg{Text: "e"})
	if gotKind != kube.KindPod || gotNamespace != "default" || gotName != "worker-0" {
		t.Fatalf("expected object-scoped events push, got kind=%q namespace=%q name=%q", gotKind, gotNamespace, gotName)
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

// TestRolloutDividerChangeOffsetAndCallout covers 16a's full-width rollout
// divider, the +CHANGE column's offset-since-rollout ("+4m"), and the
// summary strip's "first sign of trouble after rollout" correlation
// callout (docs/design README.md §16a) — all three read the same
// firstTroubleAfterRollout so they can never disagree.
func TestRolloutDividerChangeOffsetAndCallout(t *testing.T) {
	now := time.Now()
	entries := []kube.TimelineEntry{
		{Time: now.Add(-90 * time.Second), Kind: kube.TimelineEvent, Object: "Pod/api-gateway-0", Namespace: "default", Severity: "Warning", Reason: "BackOff", Message: "restarting"},
		{Time: now.Add(-6 * time.Minute), Kind: kube.TimelineRollout, Object: "Deployment/api-gateway", Namespace: "default", Reason: "Rollout", Message: "revision 5", Revision: 5, Image: "api-gateway:2.3.1"},
		{Time: now.Add(-10 * time.Minute), Kind: kube.TimelineRestart, Object: "Pod/api-gateway-0", Namespace: "default", Reason: "Restarted", Message: "app · OOMKilled · exit 137"},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{}, Namespace: "default"})
	m.SetSize(120, 36)
	updated, _ := m.Update(loadedMsg{entries: entries})
	m = *updated.(*Model)

	view := plain(m.Render())
	if !strings.Contains(view, "ROLLOUT deploy/api-gateway · rev 5 · image api-gateway:2.3.1") {
		t.Fatalf("expected the full-width rollout divider text:\n%s", view)
	}
	if !strings.Contains(view, "+4m") {
		t.Fatalf("expected the BackOff row's +CHANGE offset (+4m since the rollout):\n%s", view)
	}
	if !strings.Contains(view, "before") {
		t.Fatalf("expected the pre-rollout restart's +CHANGE to read 'before':\n%s", view)
	}
	if !strings.Contains(view, "first BackOff 4m after rollout of api-gateway") {
		t.Fatalf("expected the summary strip's correlation callout:\n%s", view)
	}
}

// TestWarningsOnlyDropsNormalEventsKeepsRestartsAndRollouts covers 16a's
// 'w' toggle (docs/design README.md §16a) — Normal-severity events are
// hard-excluded, but a restart and a rollout (never "normal" chatter)
// always survive it.
func TestWarningsOnlyDropsNormalEventsKeepsRestartsAndRollouts(t *testing.T) {
	now := time.Now()
	entries := []kube.TimelineEntry{
		{Time: now.Add(-1 * time.Minute), Kind: kube.TimelineEvent, Severity: "Normal", Reason: "Pulled", Object: "Pod/a", Namespace: "default"},
		{Time: now.Add(-2 * time.Minute), Kind: kube.TimelineEvent, Severity: "Warning", Reason: "BackOff", Object: "Pod/a", Namespace: "default"},
		{Time: now.Add(-3 * time.Minute), Kind: kube.TimelineRestart, Reason: "Restarted", Object: "Pod/a", Namespace: "default"},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{}, Namespace: "default"})
	m.SetSize(120, 36)
	updated, _ := m.Update(loadedMsg{entries: entries})
	m = *updated.(*Model)

	if len(m.rows) != 2 || len(m.foldedNormal) != 1 {
		t.Fatalf("expected 2 significant rows + 1 folded normal before 'w', got rows=%d folded=%d", len(m.rows), len(m.foldedNormal))
	}

	m = step(t, m, tea.KeyPressMsg{Text: "w"})
	if !m.warningsOnly {
		t.Fatal("expected 'w' to set warningsOnly")
	}
	if len(m.foldedNormal) != 0 {
		t.Fatalf("expected 'w' to drop the normal event entirely (not just fold it), got %+v", m.foldedNormal)
	}
	if len(m.rows) != 2 {
		t.Fatalf("expected the restart and warning to survive 'w', got %d rows", len(m.rows))
	}
}

// TestFoldedNormalFooterAndTabExpand covers 16a's collapsed "normal events"
// footer line and 'tab' expand/collapse (docs/design README.md §16a: "9b's
// own idioms... normals collapsed into one group line").
func TestFoldedNormalFooterAndTabExpand(t *testing.T) {
	now := time.Now()
	entries := []kube.TimelineEntry{
		{Time: now.Add(-1 * time.Minute), Kind: kube.TimelineEvent, Severity: "Warning", Reason: "BackOff", Object: "Pod/a", Namespace: "default"},
		{Time: now.Add(-2 * time.Minute), Kind: kube.TimelineEvent, Severity: "Normal", Reason: "Pulled", Object: "Pod/a", Namespace: "default"},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{}, Namespace: "default"})
	m.SetSize(120, 36)
	updated, _ := m.Update(loadedMsg{entries: entries})
	m = *updated.(*Model)

	if len(m.rows) != 1 || len(m.foldedNormal) != 1 {
		t.Fatalf("expected 1 significant row + 1 folded normal, got rows=%d folded=%d", len(m.rows), len(m.foldedNormal))
	}
	view := plain(m.Render())
	if !strings.Contains(view, "1 normal events — Pulled") {
		t.Fatalf("expected the folded footer line:\n%s", view)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	if !m.normalExpanded || len(m.rows) != 2 {
		t.Fatalf("expected tab to expand the normal entry into m.rows, expanded=%v rows=%d", m.normalExpanded, len(m.rows))
	}

	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	if m.normalExpanded || len(m.rows) != 1 {
		t.Fatalf("expected a second tab to re-collapse, expanded=%v rows=%d", m.normalExpanded, len(m.rows))
	}
}

// fakeRolloutMutator is a kube.Mutator test double recording RolloutUndo
// calls — every other method is a no-op stub (16b's own rollback flow is
// the only mutating verb this package's tests exercise).
type fakeRolloutMutator struct {
	called          bool
	namespace, name string
	revision        int
	err             error
}

func (f *fakeRolloutMutator) DeleteResource(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeRolloutMutator) DeleteResourceForced(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeRolloutMutator) RolloutRestart(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeRolloutMutator) Cordon(context.Context, string, bool) error { return nil }
func (f *fakeRolloutMutator) Drain(context.Context, string) (int, error) { return 0, nil }
func (f *fakeRolloutMutator) HelmRollback(context.Context, string, string, int) error {
	return nil
}
func (f *fakeRolloutMutator) RolloutUndo(_ context.Context, namespace, name string, toRevision int) error {
	f.called = true
	f.namespace, f.name, f.revision = namespace, name, toRevision
	return f.err
}
func (f *fakeRolloutMutator) Scale(context.Context, kube.ResourceKind, string, string, int32) error {
	return nil
}
func (f *fakeRolloutMutator) SetImage(context.Context, kube.ResourceKind, string, string, string, string) error {
	return nil
}
func (f *fakeRolloutMutator) SetResources(context.Context, kube.ResourceKind, string, string, string, kube.ResourceEdits, bool) error {
	return nil
}
func (f *fakeRolloutMutator) PatchMeta(context.Context, kube.ResourceKind, string, string, bool, string, string, bool) error {
	return nil
}
func (f *fakeRolloutMutator) PatchSecretData(context.Context, string, string, string, string, bool) error {
	return nil
}
func (f *fakeRolloutMutator) PatchConfigMapData(context.Context, string, string, string, string, bool) error {
	return nil
}

// railFixture builds an object-scoped Pod owned (via its ReplicaSet) by
// Deployment "nva-worker" with two rollout revisions (5 current, 4 prior) —
// shared by the rail-focus and rollback tests below.
func railFixture() (fakeLister, string) {
	rsCurrent := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nva-worker-abc123", Namespace: "default",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": "5"},
			OwnerReferences:   []metav1.OwnerReference{{Kind: "Deployment", Name: "nva-worker"}},
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: "nginx:1.27"}},
		}}},
	}
	rsPrev := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nva-worker-old1", Namespace: "default",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": "4"},
			OwnerReferences:   []metav1.OwnerReference{{Kind: "Deployment", Name: "nva-worker"}},
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: "nginx:1.26"}},
		}}},
	}
	pod := testPod("nva-worker-9k2ss", "node-a", 0)
	pod.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "nva-worker-abc123"}}
	return fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod:        {pod},
		kube.KindReplicaSet: {rsCurrent, rsPrev},
	}}, "nva-worker-9k2ss"
}

// TestRailFocusAndRollbackFlow covers 16b's rail-focused-by-default model
// (the rail is why you pressed 't' on a workload, so it starts focused —
// 'tab' moves focus onto the feed instead) end-to-end through a non-prod
// rollback.
func TestRailFocusAndRollbackFlow(t *testing.T) {
	lister, podName := railFixture()
	mut := &fakeRolloutMutator{}
	m := New(Config{
		Session: newSession(), Events: fakeEvents{}, Lister: lister, Mutator: mut,
		Namespace: "default", ObjectKind: kube.KindPod, ObjectName: podName,
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if len(m.rail) != 2 {
		t.Fatalf("expected a 2-entry rail, got %+v", m.rail)
	}
	if !m.railFocused {
		t.Fatal("expected the rail to start focused")
	}

	m = step(t, m, tea.KeyPressMsg{Text: "down"})
	if m.railSelected != 1 {
		t.Fatalf("expected down to move the rail cursor to revision 4 (index 1), got railSelected=%d", m.railSelected)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "R"})
	if !m.actionsCtl.Active() {
		t.Fatal("expected R to begin the rollback confirmation")
	}
	pending := m.actionsCtl.Pending()
	if pending == nil || pending.Scope.Verb != "rollout-undo" || pending.Scope.ResourceName != "nva-worker" || pending.Scope.Revision != 4 {
		t.Fatalf("unexpected pending action: %+v", pending)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if !mut.called || mut.namespace != "default" || mut.name != "nva-worker" || mut.revision != 4 {
		t.Fatalf("expected RolloutUndo(default, nva-worker, 4), got called=%v ns=%q name=%q rev=%d", mut.called, mut.namespace, mut.name, mut.revision)
	}
}

// TestRolloutUndoInProdRequiresTypedName mirrors poddetail's own
// TestDeleteInProdRequiresTypedName for 16b's rollback: a PROD context
// escalates to the type-the-deployment-name modal, and a bare "y" types
// into the buffer rather than confirming (docs/design README.md §16b).
func TestRolloutUndoInProdRequiresTypedName(t *testing.T) {
	lister, podName := railFixture()
	mut := &fakeRolloutMutator{}
	sess := newSession()
	sess.Config = config.Config{ProdContexts: []string{sess.Location.Context}}
	m := New(Config{
		Session: sess, Events: fakeEvents{}, Lister: lister, Mutator: mut,
		Namespace: "default", ObjectKind: kube.KindPod, ObjectName: podName,
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "down"})
	m = step(t, m, tea.KeyPressMsg{Text: "R"})
	if !m.actionsCtl.Active() || m.actionsCtl.Tier() != actions.TierModal {
		t.Fatalf("expected R in a prod context to open the type-the-name modal, tier=%v", m.actionsCtl.Tier())
	}
	view := plain(m.Render())
	if !strings.Contains(view, `type "nva-worker" to confirm`) || !strings.Contains(view, "rollback (when name matches)") {
		t.Fatalf("expected the type-the-name modal actually rendered in Body():\n%s", view)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if mut.called {
		t.Fatal("expected 'y' to type, not confirm, in the modal")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if mut.called {
		t.Fatal("expected enter to no-op before the name matches")
	}

	// esc cancels the "y"-tainted buffer; re-opening starts fresh so the
	// real name below isn't prefixed with that stray "y".
	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.actionsCtl.Active() {
		t.Fatal("expected esc to cancel the modal")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "R"})
	for _, r := range "nva-worker" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if !mut.called || mut.revision != 4 {
		t.Fatalf("expected RolloutUndo to fire once the name matches, got called=%v rev=%d", mut.called, mut.revision)
	}
}

// TestRailSelectionLiveSyncsFeedCursor covers 16b's rail↔feed live sync: the
// rail starts focused (point 1) with the feed cursor already synced onto the
// current revision, and moving the rail cursor moves the feed cursor onto
// that revision's own ROLLOUT row immediately — no '↵' needed — widening the
// window to "all time" when the target revision's rollout (2h old,
// railFixture) falls outside the default 30m window, and focus stays on the
// rail throughout so ↑↓ keeps driving revision selection.
func TestRailSelectionLiveSyncsFeedCursor(t *testing.T) {
	lister, podName := railFixture()
	m := New(Config{
		Session: newSession(), Events: fakeEvents{}, Lister: lister,
		Namespace: "default", ObjectKind: kube.KindPod, ObjectName: podName,
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if !m.railFocused {
		t.Fatal("expected the rail to start focused")
	}
	got, ok := m.selectedRow()
	if !ok || got.Kind != kube.TimelineRollout || got.Revision != 5 {
		t.Fatalf("expected the feed cursor already on revision 5's rollout row, got %+v ok=%v", got, ok)
	}

	if idx := m.indexOfEntry(m.rail[1]); idx >= 0 {
		t.Fatalf("expected revision 4's rollout outside the default window before moving the rail cursor, found at %d", idx)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "down"})
	if !m.railFocused {
		t.Fatal("expected focus to stay on the rail after moving the cursor")
	}
	if m.railSelected != 1 {
		t.Fatalf("expected the rail cursor on revision 4 (index 1), got %d", m.railSelected)
	}
	if m.window != 0 {
		t.Fatalf("expected moving onto revision 4 to widen the window to all time, got %v", m.window)
	}
	got, ok = m.selectedRow()
	if !ok || got.Kind != kube.TimelineRollout || got.Revision != 4 {
		t.Fatalf("expected the feed cursor to live-sync onto revision 4's rollout row, got %+v ok=%v", got, ok)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "up"})
	got, ok = m.selectedRow()
	if !ok || got.Kind != kube.TimelineRollout || got.Revision != 5 {
		t.Fatalf("expected moving back up to live-sync the feed cursor onto revision 5, got %+v ok=%v", got, ok)
	}
}

// TestRailSelectionSyncsToLatestEventNotJustRollout covers the rest of
// point 2: when the selected revision's own lifetime window contains a real
// entry (a restart, here), the live sync lands the feed cursor on that
// entry — the most recent thing that actually happened under the
// revision — rather than always landing on the revision's own ROLLOUT row.
func TestRailSelectionSyncsToLatestEventNotJustRollout(t *testing.T) {
	lister, podName := railFixture()
	m := New(Config{
		Session: newSession(), Events: fakeEvents{}, Lister: lister,
		Namespace: "default", ObjectKind: kube.KindPod, ObjectName: podName,
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	// A restart landing inside revision 4's own window (its rollout up to
	// revision 5's).
	restart := kube.TimelineEntry{
		Time: m.rail[1].Time.Add(10 * time.Minute), Kind: kube.TimelineRestart,
		Object: "Pod/" + podName, Namespace: "default", Reason: "Restarted", Message: "OOMKilled",
	}
	m.entries = append(m.entries, restart)

	m = step(t, m, tea.KeyPressMsg{Text: "down"})
	got, ok := m.selectedRow()
	if !ok || got.Kind != kube.TimelineRestart {
		t.Fatalf("expected the feed cursor to sync onto the restart under revision 4, not the rollout row, got %+v ok=%v", got, ok)
	}
}

// TestShortImageDropsRegistryPrefix covers point 3: only the trailing
// "name:tag" component of an image reference is ever shown — an internal
// registry host (with its own port, common for self-hosted registries)
// never belongs in a 60-column sidebar.
func TestShortImageDropsRegistryPrefix(t *testing.T) {
	cases := map[string]string{
		"r.vayner.systems:30080/aim/aim.bp.app:5.31.0.58108": "aim.bp.app:5.31.0.58108",
		"gcr.io/my-project/my-image:tag":                     "my-image:tag",
		"nginx:1.27":                                         "nginx:1.27",
		"checkout-api":                                       "checkout-api",
	}
	for in, want := range cases {
		if got := shortImage(in); got != want {
			t.Errorf("shortImage(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRailCardShowsShortImage covers point 3 end-to-end: the rail sidebar
// itself renders the shortened image, not the full registry-qualified
// reference.
func TestRailCardShowsShortImage(t *testing.T) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nva-worker-abc123", Namespace: "default",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": "4"},
			OwnerReferences:   []metav1.OwnerReference{{Kind: "Deployment", Name: "nva-worker"}},
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: "r.vayner.systems:30080/aim/aim.bp.app:5.31.0.58108"}},
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

	view := plain(m.Render())
	if !strings.Contains(view, "aim.bp.app:5.31.0.58108") {
		t.Fatalf("expected the short image name in view:\n%s", view)
	}
	if strings.Contains(view, "r.vayner.systems") {
		t.Fatalf("expected the registry host to be dropped from view:\n%s", view)
	}
}

// TestDeploymentScopedTimelineIncludesPodEvents is a regression test for a
// live bug report: opening 16b's Timeline from a Deployment showed only
// rollouts, because a container-level event like CreateContainerError is
// always emitted with involvedObject == the Pod, never the owning
// Deployment — ObjectEvents(Deployment) alone can never see it.
// eventsForScope's owned-pods fan-out (load.go) is what closes this gap.
func TestDeploymentScopedTimelineIncludesPodEvents(t *testing.T) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "checkout-api-7d9f6c8b95", Namespace: "default",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": "1"},
			OwnerReferences:   []metav1.OwnerReference{{Kind: "Deployment", Name: "checkout-api"}},
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: "checkout-api:1.9.0"}},
		}}},
	}
	pod := testPod("checkout-api-7d9f6c8b95-k2m9x", "node-a", 0)
	pod.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "checkout-api-7d9f6c8b95"}}
	otherPod := testPod("other-app-abcde", "node-a", 0)

	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod:        {pod, otherPod},
		kube.KindReplicaSet: {rs},
	}}
	events := []kube.Event{
		{Type: "Warning", Reason: "CreateContainerError", Object: "Pod/checkout-api-7d9f6c8b95-k2m9x", Message: "container create failed", Count: 1, LastSeen: time.Now()},
		{Type: "Warning", Reason: "BackOff", Object: "Pod/other-app-abcde", Message: "unrelated pod's own event", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{
		Session: newSession(), Events: fakeEvents{events: events}, Lister: lister,
		Namespace: "default", ObjectKind: kube.KindDeployment, ObjectName: "checkout-api",
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	view := plain(m.Render())
	if !strings.Contains(view, "CreateContainerError") {
		t.Fatalf("expected the owned pod's CreateContainerError event in view:\n%s", view)
	}
	if strings.Contains(view, "unrelated pod's own event") {
		t.Fatalf("expected an unrelated pod's event to stay excluded:\n%s", view)
	}
}
