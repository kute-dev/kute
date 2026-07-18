package browse

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

func nodeObj(name string, ready bool, cordoned bool) *corev1.Node {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.30.1"},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
		},
	}
}

// fakeMutator is a minimal kube.Mutator test double recording delete/
// cordon/drain calls, for browse's 8b/11a ctrl-d/C/D key tests.
type fakeMutator struct {
	cordoned     map[string]bool
	drained      []string
	deleted      []string
	forceDeleted []string
	scaled       []int32
	err          error
}

func (f *fakeMutator) DeleteResource(_ context.Context, _ kube.ResourceKind, _ string, name string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, name)
	return nil
}
func (f *fakeMutator) DeleteResourceForced(_ context.Context, _ kube.ResourceKind, _ string, name string) error {
	if f.err != nil {
		return f.err
	}
	f.forceDeleted = append(f.forceDeleted, name)
	return nil
}
func (f *fakeMutator) RolloutRestart(context.Context, string, string) error { return f.err }
func (f *fakeMutator) Cordon(_ context.Context, node string, cordon bool) error {
	if f.err != nil {
		return f.err
	}
	if f.cordoned == nil {
		f.cordoned = map[string]bool{}
	}
	f.cordoned[node] = cordon
	return nil
}
func (f *fakeMutator) Drain(_ context.Context, node string) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.drained = append(f.drained, node)
	return 2, nil
}
func (f *fakeMutator) HelmRollback(context.Context, string, string, int) error { return f.err }
func (f *fakeMutator) Scale(_ context.Context, _ kube.ResourceKind, _, _ string, replicas int32) error {
	if f.err != nil {
		return f.err
	}
	f.scaled = append(f.scaled, replicas)
	return nil
}

func TestNodeColumnsRenderStatusPodsAndVersion(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {nodeObj("node-a", true, false)},
		kube.KindPod:  {pod("default", "api-1")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	view := plain(m.Render())
	for _, want := range []string{"node-a", "Ready", "PODS", "VERSION", "v1.30.1", "cluster-scoped"} {
		if !strings.Contains(view, want) {
			t.Fatalf("node view missing %q:\n%s", want, view)
		}
	}
}

func TestCKeyCordonsAndUncordonsNode(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {nodeObj("node-a", true, false)},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "C"})
	if cordoned, ok := mut.cordoned["node-a"]; !ok || !cordoned {
		t.Fatalf("expected node-a cordoned=true, got %v", mut.cordoned)
	}
	if m.state != tui.TaskStateReady {
		t.Fatalf("expected state to return to ready after cordon, got %s", m.state)
	}
}

func TestCKeyOnCordonedNodeUncordons(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {nodeObj("node-a", true, true)}, // already cordoned
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "C"})
	if cordoned, ok := mut.cordoned["node-a"]; !ok || cordoned {
		t.Fatalf("expected node-a cordoned=false (uncordon), got %v", mut.cordoned)
	}
}

func schedPod(ns, name, node string) *corev1.Pod {
	p := pod(ns, name)
	p.Spec.NodeName = node
	return p
}

func TestDKeyShowsDrainConfirmAndYExecutes(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {nodeObj("node-a", true, false)},
		kube.KindPod: {
			schedPod("default", "p1", "node-a"), schedPod("default", "p2", "node-a"),
		},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "D"})
	if !m.actions.Active() {
		t.Fatal("expected D to open a drain confirmation")
	}
	view := plain(m.Render())
	if !strings.Contains(view, "node-a") || !strings.Contains(view, "2 pods will be evicted") {
		t.Fatalf("drain confirm missing evicted-pod count:\n%s", view)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.drained) != 1 || mut.drained[0] != "node-a" {
		t.Fatalf("expected node-a drained, got %v", mut.drained)
	}
}

func TestDKeyThenNCancelsWithoutDraining(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {nodeObj("node-a", true, false)},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "D"})
	m = step(t, m, tea.KeyPressMsg{Text: "n"})
	if m.actions.Active() {
		t.Fatal("expected n to cancel the drain confirmation")
	}
	if len(mut.drained) != 0 {
		t.Fatalf("expected no drain, got %v", mut.drained)
	}
}

func TestNodeHealthStripShowsReadyPressureCordoned(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {
			nodeObj("node-a", true, false),
			nodeObj("node-b", true, true), // cordoned
		},
	}}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	view := plain(m.Render())
	for _, want := range []string{"ready", "cordoned", "2 nodes"} {
		if !strings.Contains(view, want) {
			t.Fatalf("node health strip missing %q:\n%s", want, view)
		}
	}
}

func TestEnterOpensNodeDetail(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {nodeObj("node-a", true, false)},
	}}
	var openedNode string
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{
		Session: session, Lister: lister,
		OpenNodeDetail: func(name string, w, h int) (tea.Model, tea.Cmd) {
			openedNode = name
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	if openedNode != "node-a" {
		t.Fatalf("expected node-a to be opened, got %q", openedNode)
	}
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected Update to return the pushed stub task, got %T", updated)
	}
}

// stubTask is a minimal tea.Model standing in for a pushed screen.
type stubTask struct{}

func (stubTask) Init() tea.Cmd                       { return nil }
func (stubTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return stubTask{}, nil }
func (stubTask) View() tea.View                      { return tea.NewView("") }
