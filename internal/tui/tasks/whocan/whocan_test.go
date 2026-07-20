package whocan

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

func plain(s string) string { return ansi.Strip(s) }

type fakeRBAC struct {
	result kube.WhoCanResult
	err    error
	calls  []kube.WhoCanQuery
}

func (f *fakeRBAC) WhoCan(_ context.Context, query kube.WhoCanQuery) (kube.WhoCanResult, error) {
	f.calls = append(f.calls, query)
	return f.result, f.err
}

func newSession() *tui.Session {
	return &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "test-cluster"}}
}

// step mirrors events_test.go's own helper: applies one message, draining
// any resulting tea.BatchMsg fan-out, so Init()'s tea.Batch(load(),
// SpinnerTick()) resolves synchronously in tests.
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

func TestLoadResolvesSubjectsAndPinsGrantedCurrentUser(t *testing.T) {
	rbac := &fakeRBAC{result: kube.WhoCanResult{
		Subjects: []kube.WhoCanSubject{
			{Name: "dev-readonly", Kind: "User", Via: "clusterrole/view ← clusterrolebinding/viewers", ClusterScope: true},
			{Name: "bob", Kind: "User", Via: "role/secret-reader ← rolebinding/secret-readers"},
		},
		CurrentUser:        "dev-readonly",
		CurrentUserGranted: true,
	}}
	m := New(Config{Session: newSession(), RBAC: rbac, Verb: "list", Resource: "secrets", Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready", m.state)
	}
	if len(rbac.calls) != 1 || rbac.calls[0].Verb != "list" || rbac.calls[0].Resource != "secrets" || rbac.calls[0].Namespace != "default" {
		t.Fatalf("unexpected WhoCan call(s): %+v", rbac.calls)
	}
	// dev-readonly's own subject row is promoted to the pinned first row and
	// dropped from the plain list below it, so it appears exactly once.
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want 2 (pinned dev-readonly + bob)", len(m.rows))
	}
	if !m.rows[0].pinned || !m.rows[0].granted || m.rows[0].subject.Name != "dev-readonly" {
		t.Fatalf("rows[0] = %+v, want the pinned granted dev-readonly row", m.rows[0])
	}
	if m.rows[1].pinned || m.rows[1].subject.Name != "bob" {
		t.Fatalf("rows[1] = %+v, want bob", m.rows[1])
	}

	view := plain(m.Render())
	if !strings.Contains(view, "who can") || !strings.Contains(view, "list") || !strings.Contains(view, "secrets") {
		t.Fatalf("expected the question strip in view:\n%s", view)
	}
	if !strings.Contains(view, "dev-readonly (you)") {
		t.Fatalf("expected the pinned (you) row in view:\n%s", view)
	}
	if !strings.Contains(view, "kubectl auth can-i list secrets") {
		t.Fatalf("expected the same-as strip in view:\n%s", view)
	}
}

func TestDeniedCurrentUserGetsRedPinnedRowWithClosestMiss(t *testing.T) {
	rbac := &fakeRBAC{result: kube.WhoCanResult{
		Subjects:           []kube.WhoCanSubject{{Name: "bob", Kind: "User", Via: "role/secret-reader ← rolebinding/secret-readers"}},
		CurrentUser:        "dev-readonly",
		CurrentUserGranted: false,
		CurrentUserVia:     "clusterrole/view grants get, list on pods — not secrets",
	}}
	m := New(Config{Session: newSession(), RBAC: rbac, Verb: "list", Resource: "secrets", Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if len(m.rows) != 2 || !m.rows[0].pinned || m.rows[0].granted {
		t.Fatalf("expected a denied pinned row first, got %+v", m.rows)
	}
	view := plain(m.Render())
	if !strings.Contains(view, "clusterrole/view grants get, list on pods") {
		t.Fatalf("expected the closest-miss VIA text in view:\n%s", view)
	}
}

func TestEmptyResultEntersEmptyState(t *testing.T) {
	rbac := &fakeRBAC{result: kube.WhoCanResult{}}
	m := New(Config{Session: newSession(), RBAC: rbac, Verb: "delete", Resource: "namespaces", Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty", m.state)
	}
	view := plain(m.Render())
	if !strings.Contains(view, "no subject can delete namespaces") {
		t.Fatalf("expected the empty explainer in view:\n%s", view)
	}
}

func TestSetVerbAndResourceMsgsReload(t *testing.T) {
	rbac := &fakeRBAC{result: kube.WhoCanResult{Subjects: []kube.WhoCanSubject{{Name: "alice", Kind: "User"}}}}
	m := New(Config{Session: newSession(), RBAC: rbac, Verb: "list", Resource: "pods", Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tui.SetWhoCanVerbMsg{Verb: "delete"})
	m = step(t, m, tui.SetWhoCanResourceMsg{Resource: "secrets"})
	m = step(t, m, tui.SwitchNamespaceMsg{Namespace: "staging"})

	if v, r := m.WhoCanQuery(); v != "delete" || r != "secrets" {
		t.Fatalf("WhoCanQuery() = (%q, %q), want (delete, secrets)", v, r)
	}
	if m.namespace != "staging" {
		t.Fatalf("namespace = %q, want staging", m.namespace)
	}
	last := rbac.calls[len(rbac.calls)-1]
	if last.Verb != "delete" || last.Resource != "secrets" || last.Namespace != "staging" {
		t.Fatalf("last WhoCan call = %+v, want delete/secrets/staging", last)
	}
}

func TestEnterOpensSelectedBindingYAML(t *testing.T) {
	rbac := &fakeRBAC{result: kube.WhoCanResult{
		Subjects: []kube.WhoCanSubject{
			{Name: "bob", Kind: "User", BindingKind: kube.KindRoleBinding, BindingNamespace: "default", BindingName: "secret-readers"},
		},
	}}
	var openedKind kube.ResourceKind
	var openedNS, openedName string
	m := New(Config{
		Session: newSession(), RBAC: rbac, Verb: "list", Resource: "secrets", Namespace: "default",
		OpenYAML: func(kind kube.ResourceKind, ns, name string, w, h int) (tea.Model, tea.Cmd) {
			openedKind, openedNS, openedName = kind, ns, name
			return &fakeTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	if _, ok := updated.(*fakeTask); !ok {
		t.Fatalf("expected Enter to push the YAML task, got %T", updated)
	}
	if openedKind != kube.KindRoleBinding || openedNS != "default" || openedName != "secret-readers" {
		t.Fatalf("openYAML called with (%s, %s, %s), want (RoleBinding, default, secret-readers)", openedKind, openedNS, openedName)
	}
}

// TestRendersInBothThemes is the CLAUDE.md "both themes always" smoke check
// every screen owes, independent of golden_test.go's own dark/light
// truecolor fixtures — just a render that doesn't panic and produces real
// content under both palettes, exercised against a different fixture shape
// than the golden suite's.
func TestRendersInBothThemes(t *testing.T) {
	rbac := &fakeRBAC{result: kube.WhoCanResult{
		Subjects:           []kube.WhoCanSubject{{Name: "bob", Kind: "User", Via: "role/secret-reader ← rolebinding/secret-readers"}},
		CurrentUser:        "dev-readonly",
		CurrentUserGranted: false,
		CurrentUserVia:     "clusterrole/view grants get, list on pods — not secrets",
	}}
	for _, theme := range []tui.Theme{tui.Dark(), tui.Light()} {
		sess := newSession()
		sess.Theme = theme
		m := New(Config{Session: sess, RBAC: rbac, Verb: "list", Resource: "secrets", Namespace: "default"})
		m.SetSize(120, 36)
		m = step(t, m, m.Init()())
		view := plain(m.Render())
		if !strings.Contains(view, "WHO CAN") || !strings.Contains(view, "bob") {
			t.Fatalf("theme render missing expected content:\n%s", view)
		}
	}
}

// fakeTask is a minimal tea.Model stand-in for asserting Update pushed a
// new task (mirrors poddetail/routetable's own such-and-such push tests).
type fakeTask struct{}

func (f *fakeTask) Init() tea.Cmd                       { return nil }
func (f *fakeTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return f, nil }
func (f *fakeTask) View() tea.View                      { return tea.NewView("") }
