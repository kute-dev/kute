package helmhistory

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

func plain(s string) string { return ansi.Strip(s) }

type fakeLister struct {
	objs map[kube.ResourceKind][]runtime.Object
}

func (f fakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	return f.objs[kind], nil
}

type fakeMutator struct {
	namespace, name string
	revision        int
	err             error
}

func (f *fakeMutator) DeleteResource(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeMutator) DeleteResourceForced(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeMutator) RolloutRestart(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeMutator) Cordon(context.Context, string, bool) error { return nil }
func (f *fakeMutator) Drain(context.Context, string) (int, error) { return 0, nil }
func (f *fakeMutator) Scale(context.Context, kube.ResourceKind, string, string, int32) error {
	return nil
}
func (f *fakeMutator) SetImage(context.Context, kube.ResourceKind, string, string, string, string) error {
	return nil
}
func (f *fakeMutator) SetResources(context.Context, kube.ResourceKind, string, string, string, kube.ResourceEdits, bool) error {
	return nil
}
func (f *fakeMutator) PatchMeta(context.Context, kube.ResourceKind, string, string, bool, string, string, bool) error {
	return nil
}
func (f *fakeMutator) PatchSecretData(context.Context, string, string, string, string, bool) error {
	return nil
}
func (f *fakeMutator) PatchConfigMapData(context.Context, string, string, string, string, bool) error {
	return nil
}
func (f *fakeMutator) HelmRollback(_ context.Context, namespace, name string, revision int) error {
	f.namespace, f.name, f.revision = namespace, name, revision
	return f.err
}
func (f *fakeMutator) RolloutUndo(context.Context, string, string, int) error { return nil }

func newSession() *tui.Session {
	return &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "test-cluster"}}
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

func revisionSecret(namespace, name, status string, revision int) *corev1.Secret {
	return kube.EncodeHelmReleaseSecret(kube.HelmRelease{
		Namespace: namespace, Name: name, Chart: "postgresql", ChartVersion: "12.1.9",
		Revision: revision, Status: status,
	})
}

func TestLoadSortsRevisionsNewestFirst(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {
			revisionSecret("production", "postgresql", "superseded", 1),
			revisionSecret("production", "postgresql", "deployed", 3),
			revisionSecret("production", "postgresql", "superseded", 2),
			revisionSecret("production", "other-release", "deployed", 1),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("expected ready state, got %s (feedback=%q)", m.state, m.feedback)
	}
	if len(m.revisions) != 3 {
		t.Fatalf("expected 3 revisions for postgresql, got %d", len(m.revisions))
	}
	for i, want := range []int{3, 2, 1} {
		if m.revisions[i].Revision != want {
			t.Fatalf("revisions[%d].Revision = %d, want %d", i, m.revisions[i].Revision, want)
		}
	}
	body := plain(m.railBody(m.Theme(), 120, 20))
	if !strings.Contains(body, "(current)") {
		t.Fatalf("expected the current revision marked, got:\n%s", body)
	}
}

func TestRollbackToSelectedRevisionConfirmsAndExecutes(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {
			revisionSecret("production", "postgresql", "superseded", 1),
			revisionSecret("production", "postgresql", "deployed", 2),
		},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m.moveSelection(1) // select revision 1 (the older one)
	if rev, ok := m.selectedRevision(); !ok || rev.Revision != 1 {
		t.Fatalf("expected revision 1 selected, got %+v (ok=%v)", rev, ok)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "R"})
	if !m.actions.Active() {
		t.Fatal("expected a pending rollback confirm after 'R'")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if mut.namespace != "production" || mut.name != "postgresql" || mut.revision != 1 {
		t.Fatalf("HelmRollback called with ns=%q name=%q rev=%d, want production/postgresql/1", mut.namespace, mut.name, mut.revision)
	}
}

// TestKeybarGoesOfflineAndHidesRollback pins the cross-cutting 4a fix
// (docs/design README.md §52, §301): helmhistory must show the OFFLINE pill
// and drop rollback from the keybar while disconnected, not just browse.
func TestKeybarGoesOfflineAndHidesRollback(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {
			revisionSecret("production", "postgresql", "superseded", 1),
			revisionSecret("production", "postgresql", "deployed", 2),
		},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "dial timeout"})
	kb := m.Keybar()
	if kb.Pill != tui.ModeOffline || kb.PillText != "OFFLINE" {
		t.Fatalf("Pill/PillText = %v/%q while offline, want ModeOffline/OFFLINE", kb.Pill, kb.PillText)
	}
	for _, g := range kb.Groups {
		for _, h := range g {
			if h.Key == verbs.Rollback.Key {
				t.Fatalf("expected rollback hint hidden while offline, got groups %+v", kb.Groups)
			}
		}
	}

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected})
	kb = m.Keybar()
	if kb.PillText != "HELM" {
		t.Fatalf("PillText = %q after reconnect, want HELM", kb.PillText)
	}
}

func TestEscReturnsToPreviousTask(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)

	_, cmd := m.Update(tea.KeyPressMsg{Text: "esc"})
	if cmd == nil {
		t.Fatal("expected a Cmd from esc")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatalf("expected esc to produce tui.BackMsg, got %T", cmd())
	}
}
