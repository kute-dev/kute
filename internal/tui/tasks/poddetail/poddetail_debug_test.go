package poddetail

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// fakeShellDetector answers DetectShells from a fixed per-container map —
// missing entries report a real "no shell" (nil, nil), matching
// kube.DetectShells' own contract. Mirrors browse's own fakeShellDetector.
type fakeShellDetector map[string][]string

func (f fakeShellDetector) DetectShells(_ context.Context, _, _, container string) ([]string, error) {
	return f[container], nil
}

// TestDistrolessSingleContainerRoutesToDebugPanel confirms §41a's fork on
// poddetail: a single-container pod with no shell in it, once Shells is
// wired, routes to the debug panel instead of execing blind.
func TestDistrolessSingleContainerRoutesToDebugPanel(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	var gotName string
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{},
		OpenDebug: func(_ string, name string, _ []kube.ContainerInfo, _ string, _ bool, _, _ int) (tea.Model, tea.Cmd) {
			gotName = name
			return sentinelTask{}, nil
		},
		Namespace: "default", Name: "api-0",
	})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("expected poddetail to stay active during the async probe, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil probe Cmd")
	}
	updated, _ = m.Update(cmd())
	if _, ok := updated.(sentinelTask); !ok {
		t.Fatalf("expected the debug panel's sentinel task to be pushed, got %T", updated)
	}
	if gotName != "api-0" {
		t.Fatalf("OpenDebug called with name %q, want api-0", gotName)
	}
}

// TestAllShelllessMultiContainerSkipsPicker mirrors browse's own test of
// the same name: the picker is never pushed when every container is
// shell-less.
func TestAllShelllessMultiContainerSkipsPicker(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {multiContainerPod("api-0", "default", "node-a")},
	}}
	pickerCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{},
		OpenExec: func(string, string, []kube.ContainerInfo, int, int) (tea.Model, tea.Cmd) {
			pickerCalled = true
			return sentinelTask{}, nil
		},
		OpenDebug: func(string, string, []kube.ContainerInfo, string, bool, int, int) (tea.Model, tea.Cmd) {
			return sentinelTask{}, nil
		},
		Namespace: "default", Name: "api-0",
	})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	updated, _ := m.Update(cmd())
	if pickerCalled {
		t.Error("execpicker must not be pushed when every container is shell-less")
	}
	if _, ok := updated.(sentinelTask); !ok {
		t.Fatalf("expected the debug panel's sentinel task to be pushed, got %T", updated)
	}
}

// fakeRBACChecker answers WhoCan with a fixed result and records the query
// it was asked — mirrors browse's own fakeRBACChecker (no KindSynced/
// KindError methods, so tui.KindsSynced treats it as never needing the sync
// gate).
type fakeRBACChecker struct {
	result   kube.WhoCanResult
	err      error
	gotQuery kube.WhoCanQuery
}

func (f *fakeRBACChecker) WhoCan(_ context.Context, query kube.WhoCanQuery) (kube.WhoCanResult, error) {
	f.gotQuery = query
	return f.result, f.err
}

// TestDebugCapabilityDeniedBlocksPanel mirrors browse's own test of the
// same name: §41a's RBAC pre-check stops the debug panel from opening,
// surfaces the verbatim reason plus a 'w who-can' hint, and queries the
// attach-mode resource since the pod is running.
func TestDebugCapabilityDeniedBlocksPanel(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	rbac := &fakeRBACChecker{result: kube.WhoCanResult{
		CurrentUser:        "dev@example.com",
		CurrentUserGranted: false,
		CurrentUserVia:     "role/viewer grants get, list on pods — not create",
	}}
	debugCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{},
		RBAC:   rbac,
		OpenDebug: func(string, string, []kube.ContainerInfo, string, bool, int, int) (tea.Model, tea.Cmd) {
			debugCalled = true
			return sentinelTask{}, nil
		},
		Namespace: "default", Name: "api-0",
	})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	updated, _ := m.Update(cmd())
	mm, ok := updated.(*Model)
	if !ok {
		t.Fatalf("expected poddetail to stay active on a denied check, got %T", updated)
	}
	if debugCalled {
		t.Fatal("expected OpenDebug not to be called once the capability check denies it")
	}
	if mm.pendingDebugDenial == nil {
		t.Fatal("expected pendingDebugDenial to be set")
	}
	if !strings.Contains(mm.execFeedback, "not create") || !strings.Contains(mm.execFeedback, "w who-can") {
		t.Fatalf("execFeedback = %q, want the verbatim reason plus a w who-can hint", mm.execFeedback)
	}
	if rbac.gotQuery.Resource != kube.DebugAttachResource {
		t.Fatalf("WhoCan queried resource %q, want %q (pod is running)", rbac.gotQuery.Resource, kube.DebugAttachResource)
	}
}

// TestDebugCapabilityGrantedOpensPanel mirrors browse's own test: a
// granted check still opens the panel exactly as before this pre-check
// existed.
func TestDebugCapabilityGrantedOpensPanel(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	rbac := &fakeRBACChecker{result: kube.WhoCanResult{CurrentUser: "dev@example.com", CurrentUserGranted: true}}
	debugCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{},
		RBAC:   rbac,
		OpenDebug: func(string, string, []kube.ContainerInfo, string, bool, int, int) (tea.Model, tea.Cmd) {
			debugCalled = true
			return sentinelTask{}, nil
		},
		Namespace: "default", Name: "api-0",
	})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	m.Update(cmd())
	if !debugCalled {
		t.Fatal("expected OpenDebug to be called once the capability check grants it")
	}
}

// TestDebugCapabilityDeniedWOpensWhoCan mirrors browse's own test: 'w'
// opens tasks/whocan pre-filled with the exact create/resource/namespace
// query that came back denied.
func TestDebugCapabilityDeniedWOpensWhoCan(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	rbac := &fakeRBACChecker{result: kube.WhoCanResult{
		CurrentUser:        "dev@example.com",
		CurrentUserGranted: false,
		CurrentUserVia:     "no bindings grant dev@example.com access here",
	}}
	var gotVerb, gotResource, gotNamespace string
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{},
		RBAC:   rbac,
		OpenDebug: func(string, string, []kube.ContainerInfo, string, bool, int, int) (tea.Model, tea.Cmd) {
			t.Fatal("OpenDebug must not be called once the capability check denies it")
			return nil, nil
		},
		OpenWhoCan: func(verb, resource, namespace string, _, _ int) (tea.Model, tea.Cmd) {
			gotVerb, gotResource, gotNamespace = verb, resource, namespace
			return sentinelTask{}, nil
		},
		Namespace: "default", Name: "api-0",
	})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	updated, _ := m.Update(cmd())
	mm, ok := updated.(*Model)
	if !ok {
		t.Fatalf("expected poddetail to stay active on a denied check, got %T", updated)
	}

	next, _ := mm.Update(tea.KeyPressMsg{Text: "w"})
	if _, ok := next.(sentinelTask); !ok {
		t.Fatalf("expected 'w' to push tasks/whocan, got %T", next)
	}
	if gotVerb != "create" || gotResource != kube.DebugAttachResource || gotNamespace != "default" {
		t.Fatalf("openWhoCan called with (%q, %q, %q), want (create, %q, default)", gotVerb, gotResource, gotNamespace, kube.DebugAttachResource)
	}
	if mm.pendingDebugDenial != nil {
		t.Fatal("expected pendingDebugDenial to be cleared once 'w' consumes it")
	}
}
