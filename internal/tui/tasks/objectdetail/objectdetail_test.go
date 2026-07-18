package objectdetail

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

type fakeLister struct {
	objs map[kube.ResourceKind][]runtime.Object
}

func (f fakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	return f.objs[kind], nil
}

type fakeEvents struct {
	rows []kube.Event
	err  error
}

func (f fakeEvents) ObjectEvents(_ context.Context, _ string, _ kube.ResourceKind, _ string) ([]kube.Event, error) {
	return f.rows, f.err
}

func certificateKind() kube.ResourceKind { return kube.ResourceKind("Certificate") }

func testSession() *tui.Session {
	dk := kube.DiscoveredKind{
		Kind: "Certificate", Plural: "certificates", Group: "cert-manager.io",
		Versions:      []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		ClusterScoped: false,
		PrinterColumns: []kube.PrinterColumn{
			{Name: "Ready", Type: "string", JSONPath: `.status.conditions[?(@.type=="Ready")].status`},
			{Name: "Secret", Type: "string", JSONPath: ".spec.secretName"},
		},
		Established: true,
	}
	reg, _ := resources.BuildDiscoveredRegistry([]kube.DiscoveredKind{dk}, nil)
	return &tui.Session{Theme: tui.Dark(), Registry: reg, Location: tui.Location{Context: "test-cluster"}}
}

func certObj(name string, cond map[string]any) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"spec":       map[string]any{"secretName": name + "-secret"},
	}
	if cond != nil {
		obj["status"] = map[string]any{"conditions": []any{cond}}
	}
	return &unstructured.Unstructured{Object: obj}
}

// step drains a returned tea.Cmd synchronously against a *Model, mirroring
// poddetail's own test helper — objectdetail's Update has the same pointer-
// receiver shape.
func step(t *testing.T, m *Model, msg tea.Msg) (tea.Model, tea.Cmd) {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var last tea.Model = m
		for _, c := range batch {
			if c == nil {
				continue
			}
			next, cmd := step(t, last.(*Model), c())
			last = next
			if cmd != nil {
				return step(t, last.(*Model), cmd())
			}
		}
		return last, nil
	}
	return m.Update(msg)
}

func TestApplyLoadedReadyState(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		certificateKind(): {certObj("api-tls", map[string]any{"type": "Ready", "status": "True", "message": "all good"})},
	}}
	m := New(Config{
		Session: testSession(), Lister: lister, Events: fakeEvents{},
		Kind: certificateKind(), Namespace: "default", Name: "api-tls",
	})
	updated, _ := step(t, &m, m.load()())
	got := updated.(*Model)
	if got.state != tui.TaskStateReady || !got.found {
		t.Fatalf("expected ready+found, got state=%s found=%v feedback=%q", got.state, got.found, got.feedback)
	}
	if len(got.conditions) != 1 || got.conditions[0].Status != "True" {
		t.Fatalf("unexpected conditions: %+v", got.conditions)
	}
	if got.row.Cells[2] != "api-tls-secret" {
		t.Fatalf("unexpected meta cell: %+v", got.row.Cells)
	}
}

func TestApplyLoadedObjectGone(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}}
	m := New(Config{
		Session: testSession(), Lister: lister, Events: fakeEvents{},
		Kind: certificateKind(), Namespace: "default", Name: "missing",
	})
	updated, _ := step(t, &m, m.load()())
	got := updated.(*Model)
	if !got.gone || got.state != tui.TaskStateReady {
		t.Fatalf("expected gone=true/ready, got gone=%v state=%s", got.gone, got.state)
	}
}

func TestApplyLoadedEmptyObjectRedirectsToYAML(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		certificateKind(): {certObj("bare", nil)}, // no conditions
	}}
	yamlCalled := false
	m := New(Config{
		Session: testSession(), Lister: lister, Events: fakeEvents{}, // no events either
		Kind: certificateKind(), Namespace: "default", Name: "bare",
		OpenYAML: func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd) {
			yamlCalled = true
			return sentinelTask{}, nil
		},
	})
	updated, _ := step(t, &m, m.load()())
	if _, ok := updated.(*Model); ok {
		t.Fatalf("expected the empty-object redirect to swap away from *Model, still got one")
	}
	if !yamlCalled {
		t.Fatalf("expected OpenYAML to be called for an object with no conditions and no events")
	}
}

func TestApplyLoadedEventsErrorDoesNotRedirect(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		certificateKind(): {certObj("bare", nil)},
	}}
	yamlCalled := false
	m := New(Config{
		Session: testSession(), Lister: lister, Events: fakeEvents{err: context.DeadlineExceeded},
		Kind: certificateKind(), Namespace: "default", Name: "bare",
		OpenYAML: func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd) {
			yamlCalled = true
			return sentinelTask{}, nil
		},
	})
	updated, _ := step(t, &m, m.load()())
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("an events-fetch error is not \"empty\" — should not redirect to YAML")
	}
	if yamlCalled {
		t.Fatalf("OpenYAML should not be called when the events fetch merely failed")
	}
}

// sentinelTask is a minimal tea.Model distinct from *Model, so tests can
// assert a redirect actually swapped the active task.
type sentinelTask struct{}

func (sentinelTask) Init() tea.Cmd                       { return nil }
func (sentinelTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return sentinelTask{}, nil }
func (sentinelTask) View() tea.View                      { return tea.NewView("") }

func TestMoveSiblingLoadsNextObject(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		certificateKind(): {
			certObj("a", map[string]any{"type": "Ready", "status": "True"}),
			certObj("b", map[string]any{"type": "Ready", "status": "False"}),
		},
	}}
	m := New(Config{
		Session: testSession(), Lister: lister, Events: fakeEvents{},
		Kind: certificateKind(), Namespace: "default", Name: "a",
		Siblings: []string{"a", "b"}, SiblingIndex: 0,
	})
	cmd := m.moveSibling(1)
	if cmd == nil {
		t.Fatalf("expected a load cmd from moveSibling")
	}
	if m.name != "b" || m.siblingIndex != 1 {
		t.Fatalf("expected name=b index=1, got name=%s index=%d", m.name, m.siblingIndex)
	}
}
