package browse

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

func helmRelease(namespace, name, chart, chartVersion, appVersion, status string, revision int) *kube.HelmReleaseObject {
	return kube.NewHelmReleaseObject(kube.HelmRelease{
		Namespace: namespace, Name: name, Chart: chart, ChartVersion: chartVersion,
		AppVersion: appVersion, Revision: revision, Status: status,
	})
}

// TestHelmReleaseHealthStripCountsByStatus confirms 18a's health strip
// buckets deployed/pending-*/failed into OK/Warn/Fail per the design's own
// strip example ("3 deployed · 1 pending-upgrade · 1 failed").
func TestHelmReleaseHealthStripCountsByStatus(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindHelmRelease: {
			helmRelease("default", "postgresql", "postgresql", "12.1.9", "15.4.0", "deployed", 3),
			helmRelease("default", "redis", "redis", "18.1.5", "7.2.4", "deployed", 2),
			helmRelease("default", "grafana", "grafana", "7.3.0", "10.4.2", "deployed", 1),
			helmRelease("default", "prometheus", "kube-prometheus-stack", "58.2.1", "0.73.0", "pending-upgrade", 2),
			helmRelease("default", "broken-app", "mychart", "1.0.0", "2.1.0", "failed", 2),
		},
	}}
	session := newSession()
	session.Location.Kind = kube.KindHelmRelease
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("expected ready state, got %s (feedback=%q)", m.state, m.feedback)
	}
	strip := plain(m.healthStripLine(m.Theme(), 120))
	for _, want := range []string{"3", "deployed", "1", "pending-upgrade", "failed", "helm.sh/release.v1 secrets"} {
		if !strings.Contains(strip, want) {
			t.Fatalf("health strip %q missing %q", strip, want)
		}
	}
}

// TestHelmReleaseFailedStatusCellCarriesReason confirms a failed release's
// STATUS cell renders "failed · <reason>" verbatim (docs/design README.md
// §18a).
func TestHelmReleaseFailedStatusCellCarriesReason(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindHelmRelease: {kube.NewHelmReleaseObject(kube.HelmRelease{
			Namespace: "default", Name: "broken-app", Chart: "mychart", ChartVersion: "1.0.0",
			Revision: 2, Status: "failed", StatusReason: "hook timeout",
		})},
	}}
	session := newSession()
	session.Location.Kind = kube.KindHelmRelease
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	row, ok := m.selectedRow()
	if !ok {
		t.Fatal("expected a selected row")
	}
	if got, want := row.Cells[4], "failed · hook timeout"; got != want {
		t.Fatalf("STATUS cell = %q, want %q", got, want)
	}
}

// TestEnterOnHelmReleaseOpensFilteredPods confirms 18a's "↵ = objects in the
// release (filtered tables, 9a's recipe)".
func TestEnterOnHelmReleaseOpensFilteredPods(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindHelmRelease: {helmRelease("default", "postgresql", "postgresql", "12.1.9", "15.4.0", "deployed", 3)},
		kube.KindPod:         {pod("default", "postgresql-0")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindHelmRelease
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if m.kind != kube.KindPod || m.filterQuery != "postgresql" {
		t.Fatalf("expected Pods filtered by postgresql, got kind=%s filter=%q", m.kind, m.filterQuery)
	}
	if m.originName != "postgresql" || m.originKind != kube.KindHelmRelease {
		t.Fatalf("expected origin set to the release, got kind=%s name=%q", m.originKind, m.originName)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.kind != kube.KindHelmRelease {
		t.Fatalf("expected esc to switch back to Helm Releases, got %s", m.kind)
	}
}

// TestHelmReleaseValuesAndHistoryPushTasks confirms 'v'/'h' push the wired
// Open funcs with the loaded release's full data.
func TestHelmReleaseValuesAndHistoryPushTasks(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindHelmRelease: {helmRelease("default", "postgresql", "postgresql", "12.1.9", "15.4.0", "deployed", 3)},
	}}
	var gotValuesRelease kube.HelmRelease
	var gotHistoryNS, gotHistoryName string
	session := newSession()
	session.Location.Kind = kube.KindHelmRelease
	m := New(Config{
		Session: session, Lister: lister,
		OpenHelmValues: func(release kube.HelmRelease, w, h int) (tea.Model, tea.Cmd) {
			gotValuesRelease = release
			return stubTask{}, nil
		},
		OpenHelmHistory: func(namespace, name string, w, h int) (tea.Model, tea.Cmd) {
			gotHistoryNS, gotHistoryName = namespace, name
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "v"})
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected 'v' to push the values stub task, got %T", updated)
	}
	if gotValuesRelease.Name != "postgresql" || gotValuesRelease.Revision != 3 {
		t.Fatalf("OpenHelmValues got %+v", gotValuesRelease)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "h"})
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected 'h' to push the history stub task, got %T", updated)
	}
	if gotHistoryNS != "default" || gotHistoryName != "postgresql" {
		t.Fatalf("OpenHelmHistory got ns=%q name=%q", gotHistoryNS, gotHistoryName)
	}
}

// TestRollbackInlineConfirmNonProd confirms 'R' on a Helm release shows the
// inline y/N confirm (non-prod) and executes through kube.Mutator.
// HelmRollback on 'y' — "Rollback inherits 8b friction" (docs/design
// README.md §18a).
func TestRollbackInlineConfirmNonProd(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindHelmRelease: {helmRelease("default", "postgresql", "postgresql", "12.1.9", "15.4.0", "deployed", 3)},
	}}
	mut := &fakeHelmMutator{}
	session := newSession()
	session.Location.Kind = kube.KindHelmRelease
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "R"})
	if !m.actions.Active() {
		t.Fatal("expected a pending confirm after 'R'")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if mut.namespace != "default" || mut.name != "postgresql" || mut.revision != 2 {
		t.Fatalf("HelmRollback called with ns=%q name=%q rev=%d, want default/postgresql/2", mut.namespace, mut.name, mut.revision)
	}
}

// fakeHelmMutator is a minimal kube.Mutator stub recording HelmRollback
// calls — every other method is a no-op success.
type fakeHelmMutator struct {
	namespace, name string
	revision        int
}

func (f *fakeHelmMutator) DeleteResource(_ context.Context, _ kube.ResourceKind, _, _ string) error {
	return nil
}
func (f *fakeHelmMutator) DeleteResourceForced(_ context.Context, _ kube.ResourceKind, _, _ string) error {
	return nil
}
func (f *fakeHelmMutator) RolloutRestart(_ context.Context, _ kube.ResourceKind, _, _ string) error {
	return nil
}
func (f *fakeHelmMutator) Cordon(_ context.Context, _ string, _ bool) error { return nil }
func (f *fakeHelmMutator) Drain(_ context.Context, _ string) (int, error)   { return 0, nil }
func (f *fakeHelmMutator) Scale(context.Context, kube.ResourceKind, string, string, int32) error {
	return nil
}
func (f *fakeHelmMutator) SetImage(context.Context, kube.ResourceKind, string, string, string, string) error {
	return nil
}
func (f *fakeHelmMutator) SetResources(context.Context, kube.ResourceKind, string, string, string, kube.ResourceEdits, bool) error {
	return nil
}
func (f *fakeHelmMutator) PatchMeta(context.Context, kube.ResourceKind, string, string, bool, string, string, bool) error {
	return nil
}
func (f *fakeHelmMutator) PatchSecretData(context.Context, string, string, string, string, bool) error {
	return nil
}
func (f *fakeHelmMutator) PatchConfigMapData(context.Context, string, string, string, string, bool) error {
	return nil
}
func (f *fakeHelmMutator) HelmRollback(_ context.Context, namespace, name string, revision int) error {
	f.namespace, f.name, f.revision = namespace, name, revision
	return nil
}
func (f *fakeHelmMutator) RolloutUndo(context.Context, string, string, int) error { return nil }
