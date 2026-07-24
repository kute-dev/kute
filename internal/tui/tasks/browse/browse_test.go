package browse

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

// fakeLister is namespace-aware (unlike the other tasks' fakeLister
// fixtures) because the 10c ways-out need real per-namespace counts.
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
		if a, err := apimeta.Accessor(o); err == nil && a.GetNamespace() == namespace {
			out = append(out, o)
		}
	}
	return out, nil
}

func plain(s string) string { return ansi.Strip(s) }

func pod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}},
	}
}

func namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive}}
}

func configMap(ns, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
}

func secret(ns, name string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
}

func newSession() *tui.Session {
	return &tui.Session{
		Registry: resources.DefaultRegistry(),
		Location: tui.Location{Context: "microk8s-cluster", Namespace: "default", Kind: kube.KindPod},
		Theme:    tui.Dark(),
	}
}

// step applies one message and returns the updated Model, recursively
// draining any resulting command — including tea.Batch's fan-out, which the
// real bubbletea runtime unpacks but a direct m.Update(cmd()) call won't.
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

func TestEmptyNamespaceShowsLiveWaysOut(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("nva-stage", "api-1"),
			pod("nva-stage", "api-2"),
		},
		kube.KindNamespace: {
			namespace("default"),
			namespace("nva-stage"),
		},
		kube.KindConfigMap: {
			configMap("default", "app-config"),
			configMap("default", "other-config"),
		},
		kube.KindSecret: {
			secret("default", "app-secret"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned no load command")
	}
	m = step(t, m, cmd())

	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty", m.state)
	}

	view := plain(m.Render())
	for _, want := range []string{
		"no pods in default",
		"the namespace exists and you can read it",
		"switch namespace",
		"nva-stage has 2 pods",
		"all namespaces",
		"2 pods cluster-wide",
		"other kinds",
		"2 configmaps, 1 secret",
		"0 pods · watching",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestEmptyNamespaceWithNoSuggestionsDegradesGracefully(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespace("default")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty", m.state)
	}
	view := plain(m.Render())
	if !strings.Contains(view, "no pods in default") {
		t.Fatalf("missing empty title:\n%s", view)
	}
	if !strings.Contains(view, "switch namespace") || !strings.Contains(view, "all namespaces") || !strings.Contains(view, "other kinds") {
		t.Fatalf("missing a way-out line:\n%s", view)
	}
	// No data anywhere else, so none of the way-out lines should grow a
	// "— ..." live-data detail.
	for _, unwanted := range []string{"has 0 pods", "cluster-wide", "this namespace has"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("expected no live-data details when nothing else has resources, found %q:\n%s", unwanted, view)
		}
	}
}

func TestClusterScopedEmptyHasNoNamespaceWaysOut(t *testing.T) {
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: fakeLister{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty", m.state)
	}
	view := plain(m.Render())
	if strings.Contains(view, "switch namespace") || strings.Contains(view, "all namespaces") {
		t.Fatalf("cluster-scoped empty state should not offer namespace ways out:\n%s", view)
	}
}

func TestReadyStateRendersRows(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1"), pod("default", "worker-2")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready", m.state)
	}
	view := plain(m.Render())
	for _, want := range []string{"api-1", "worker-2", "RDY", "STATUS"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestLoadingStateBeforeDataArrives(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(120, 36)
	if m.state != tui.TaskStateLoading {
		t.Fatalf("state = %s, want loading", m.state)
	}
}

// TestLoadingStateRender pins 15a's shape: the shell (breadcrumb, column
// headers, keybar nav keys) paints in the first frame, the header/strip name
// what's loading, and skeleton rows fill the body — never a bare
// spinner-only blank screen (docs/design README.md §15a).
func TestLoadingStateRender(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(120, 36)
	view := plain(m.Render())

	for _, want := range []string{
		"Pods", "(g to jump)", // shell breadcrumb
		"loading pods",                                                 // header timer
		"listing pods in default…", "watch starts when the list lands", // strip
		"NAME", "RDY", "STATUS", "NODE", "AGE", // real column headers
		"– of –",                                      // placeholder footer
		"g", "goto", "n", "namespace", "c", "context", // live nav keys
		"row actions enable when data lands", // disabled-verbs note
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("loading view missing %q:\n%s", want, view)
		}
	}
	// Row-scoped verbs (delete, yaml, logs, exec…) must not appear while
	// rows don't exist yet.
	for _, unwanted := range []string{"delete", "yaml", "exec", "logs"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("loading view unexpectedly shows row verb %q:\n%s", unwanted, view)
		}
	}
}

// TestLoadingStateHeaderTimerAdvances checks the 15a header's "· 0.4s"
// counting timer actually ticks off SpinnerTickMsg rather than staying
// frozen at 0s (docs/design README.md §15a: "a counting timer instead of a
// fake progress bar").
func TestLoadingStateHeaderTimerAdvances(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.loadStartedAt = m.loadStartedAt.Add(-2 * time.Second)

	updated, _ := m.Update(spinner.TickMsg{Time: time.Now()})
	view := plain(updated.(*Model).Render())
	if !strings.Contains(view, "loading pods · 2.") {
		t.Fatalf("expected header timer to show ~2s elapsed:\n%s", view)
	}
}

func TestDownArrowMovesSelection(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1"), pod("default", "worker-2")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if updated.(*Model).selected != 1 {
		t.Fatalf("after down selection = %d, want 1", updated.(*Model).selected)
	}
}

func TestEscSendsBackMsg(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(120, 36)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatal("esc did not send BackMsg")
	}
}
