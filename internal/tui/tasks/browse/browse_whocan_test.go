package browse

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// TestGotoKindMsgWhoCanPushesWhoCanTask covers KindWhoCan's kind-name
// carve-out (docs/design README.md §22a): `g "who"` dispatches
// GotoKindMsg{Kind: KindWhoCan}, which pushes tasks/whocan directly instead
// of switchKind's usual Registry-descriptor list — 22a has nothing to list.
func TestGotoKindMsgWhoCanPushesWhoCanTask(t *testing.T) {
	var gotVerb, gotResource, gotNamespace string
	sess := newSession()
	sess.Location.Kind = kube.KindPod
	m := New(Config{
		Session: sess, Lister: fakeLister{},
		OpenWhoCan: func(verb, resource, namespace string, w, h int) (tea.Model, tea.Cmd) {
			gotVerb, gotResource, gotNamespace = verb, resource, namespace
			return &fakeWhoCanTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.desc, _ = m.session.Registry.Descriptor(kube.KindPod)

	updated, _ := m.Update(tui.GotoKindMsg{Kind: kube.KindWhoCan})
	if _, ok := updated.(*fakeWhoCanTask); !ok {
		t.Fatalf("expected GotoKindMsg{KindWhoCan} to push tasks/whocan, got %T", updated)
	}
	if gotVerb != "list" || gotResource != "pods" {
		t.Fatalf("openWhoCan called with (%q, %q), want (list, pods)", gotVerb, gotResource)
	}
	_ = gotNamespace
}

// TestPermissionDeniedWKeyPushesWhoCanPrefilled covers 4b's 'w' recovery
// key: it must pre-fill whocan with the denied load's own verb ("list")
// and resource (the kind currently showing) — docs/design README.md §22a:
// "arriving with the failed verb+resource pre-filled".
func TestPermissionDeniedWKeyPushesWhoCanPrefilled(t *testing.T) {
	msg := `User "dev-readonly" cannot list resource "secrets" in namespace "nva-stage"`
	lister := forbiddenLister{
		kind: kube.KindSecret,
		err:  apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", errors.New(msg)),
	}
	var gotVerb, gotResource, gotNamespace string
	sess := newSession()
	sess.Location.Kind = kube.KindSecret
	sess.Location.Namespace = "nva-stage"
	m := New(Config{
		Session: sess, Lister: lister,
		OpenWhoCan: func(verb, resource, namespace string, w, h int) (tea.Model, tea.Cmd) {
			gotVerb, gotResource, gotNamespace = verb, resource, namespace
			return &fakeWhoCanTask{}, nil
		},
	})
	m.namespace = "nva-stage"
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStatePermissionDenied {
		t.Fatalf("state = %s, want permission-denied", m.state)
	}

	updated, _ := m.updateKey(tea.KeyPressMsg{Text: "w"})
	if _, ok := updated.(*fakeWhoCanTask); !ok {
		t.Fatalf("expected 'w' to push tasks/whocan, got %T", updated)
	}
	if gotVerb != "list" || gotResource != "secrets" || gotNamespace != "nva-stage" {
		t.Fatalf("openWhoCan called with (%q, %q, %q), want (list, secrets, nva-stage)", gotVerb, gotResource, gotNamespace)
	}
}

type fakeWhoCanTask struct{}

func (f *fakeWhoCanTask) Init() tea.Cmd                       { return nil }
func (f *fakeWhoCanTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return f, nil }
func (f *fakeWhoCanTask) View() tea.View                      { return tea.NewView("") }
