package browse

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// fakeShellDetector answers DetectShells from a fixed per-container map —
// missing entries report a real "no shell" (nil, nil), matching
// kube.DetectShells' own contract.
type fakeShellDetector map[string][]string

func (f fakeShellDetector) DetectShells(_ context.Context, _, _, container string) ([]string, error) {
	return f[container], nil
}

// TestDistrolessSingleContainerRoutesToDebugPanel confirms §41a's fork: a
// single-container pod with no shell in it, once Shells is wired, routes to
// the debug panel instead of execing blind (the old behavior, still exact
// for a container that *has* a shell — TestExecSingleContainerRunsDirectly
// covers that unchanged fallback for when Shells isn't wired at all).
func TestDistrolessSingleContainerRoutesToDebugPanel(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithContainers("default", "api-0", "app")},
	}}
	var gotName string
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{}, // every container answers "no shell"
		OpenDebug: func(_ string, name string, _ []kube.ContainerInfo, _ string, _ bool, _, _ int) (tea.Model, tea.Cmd) {
			gotName = name
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("expected browse to stay active during the async probe, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil probe Cmd")
	}
	updated, _ = m.Update(cmd())
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected the debug panel's stub task to be pushed, got %T", updated)
	}
	if gotName != "api-0" {
		t.Fatalf("OpenDebug called with name %q, want api-0", gotName)
	}
}

// TestShellfulSingleContainerStillExecsDirectly confirms a container that
// does have a shell still execs immediately once the probe comes back, the
// same fast path as the Shells-unwired fallback.
func TestShellfulSingleContainerStillExecsDirectly(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithContainers("default", "api-0", "app")},
	}}
	debugCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{"app": {"bash"}},
		OpenDebug: func(string, string, []kube.ContainerInfo, string, bool, int, int) (tea.Model, tea.Cmd) {
			debugCalled = true
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	if cmd == nil {
		t.Fatal("expected a non-nil probe Cmd")
	}
	updated, execCmd := m.Update(cmd())
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("expected browse to stay the active task for a shell-ful container, got %T", updated)
	}
	if execCmd == nil {
		t.Fatal("expected a non-nil exec Cmd")
	}
	if debugCalled {
		t.Fatal("OpenDebug must not be called for a container with a shell")
	}
}

// TestAllShelllessMultiContainerSkipsPicker confirms the picker is never
// pushed when every container is shell-less — the debug panel opens
// directly (docs/design v.0.11.0.dc.html §41a: "a picker with nothing to
// pick is chrome").
func TestAllShelllessMultiContainerSkipsPicker(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithContainers("default", "api-0", "app", "sidecar")},
	}}
	pickerCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{},
		OpenExec: func(string, string, []kube.ContainerInfo, int, int) (tea.Model, tea.Cmd) {
			pickerCalled = true
			return stubTask{}, nil
		},
		OpenDebug: func(string, string, []kube.ContainerInfo, string, bool, int, int) (tea.Model, tea.Cmd) {
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	updated, _ := m.Update(cmd())
	if pickerCalled {
		t.Error("execpicker must not be pushed when every container is shell-less")
	}
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected the debug panel's stub task to be pushed, got %T", updated)
	}
}

// TestMixedShellsStillPushesPicker confirms a pod with at least one shell
// somewhere still gets the ordinary picker treatment.
func TestMixedShellsStillPushesPicker(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithContainers("default", "api-0", "app", "sidecar")},
	}}
	var gotContainers []kube.ContainerInfo
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{"sidecar": {"sh"}},
		OpenExec: func(_, _ string, containers []kube.ContainerInfo, _, _ int) (tea.Model, tea.Cmd) {
			gotContainers = containers
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	updated, _ := m.Update(cmd())
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected execpicker's stub task to be pushed, got %T", updated)
	}
	if len(gotContainers) != 2 {
		t.Fatalf("expected both containers handed to OpenExec, got %v", gotContainers)
	}
}

// TestDebugPanelDefaultModeFromCrashLoop confirms routePodShellsProbed
// resolves the pod's own CrashLoopBackOff reason for OpenDebug's podPhase
// argument, the signal debugpanel.New uses to default to copy mode.
func TestDebugPanelDefaultModeFromCrashLoop(t *testing.T) {
	crashPod := podWithContainers("default", "worker-0", "app")
	crashPod.Status.ContainerStatuses[0].State = corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {crashPod},
	}}
	var gotPhase string
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{},
		OpenDebug: func(_, _ string, _ []kube.ContainerInfo, podPhase string, _ bool, _, _ int) (tea.Model, tea.Cmd) {
			gotPhase = podPhase
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	m.Update(cmd())
	if gotPhase != "CrashLoopBackOff" {
		t.Fatalf("podPhase = %q, want CrashLoopBackOff", gotPhase)
	}
}

// TestDebugCopyTagRendersInPodsTable confirms §41c's "the copy also carries
// a ⚑ debug copy tag in the pods table for the rest of the session": a pod
// that Session.DebugCopies recognizes as kute's own copy-mode pod gets the
// NameSuffix tag on load, and an ordinary pod in the same list doesn't.
func TestDebugCopyTagRendersInPodsTable(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("default", "worker-0"),
			pod("default", "worker-0-debug"),
		},
	}}
	session := newSession()
	session.DebugCopies = kube.NewDebugCopyRegistry()
	session.DebugCopies.Add("default", "worker-0-debug")

	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	var gotPlain, gotDebug bool
	for _, row := range m.rows {
		switch row.Name {
		case "worker-0":
			gotPlain = true
			if row.NameSuffix != "" {
				t.Fatalf("worker-0 NameSuffix = %q, want empty — not a debug copy", row.NameSuffix)
			}
		case "worker-0-debug":
			gotDebug = true
			if row.NameSuffix != " ⚑ debug copy" {
				t.Fatalf("worker-0-debug NameSuffix = %q, want %q", row.NameSuffix, " ⚑ debug copy")
			}
		}
	}
	if !gotPlain || !gotDebug {
		t.Fatalf("expected both rows loaded, got rows=%+v", m.rows)
	}
}

// TestDebugCopyTagCoexistsWithEphemeralTag confirms a pod carrying both
// §41e's real ephemeral-container tag and §41c's client-side debug-copy fact
// gets both, without repeating the ⚑ glyph.
func TestDebugCopyTagCoexistsWithEphemeralTag(t *testing.T) {
	p := pod("default", "worker-0-debug")
	p.Status.EphemeralContainerStatuses = []corev1.ContainerStatus{{Name: "debugger"}}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {p},
	}}
	session := newSession()
	session.DebugCopies = kube.NewDebugCopyRegistry()
	session.DebugCopies.Add("default", "worker-0-debug")

	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if len(m.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(m.rows))
	}
	if got, want := m.rows[0].NameSuffix, " ⚑ · debug copy"; got != want {
		t.Fatalf("NameSuffix = %q, want %q", got, want)
	}
}

// fakeRBACChecker answers WhoCan with a fixed result and records the query
// it was asked — no KindSynced/KindError methods, so tui.KindsSynced treats
// it as never needing the sync gate (kindsync.go's own "true for any lister
// implementing neither checker" default), keeping these tests focused on
// the WhoCan verdict itself rather than cache-sync plumbing.
type fakeRBACChecker struct {
	result   kube.WhoCanResult
	err      error
	gotQuery kube.WhoCanQuery
}

func (f *fakeRBACChecker) WhoCan(_ context.Context, query kube.WhoCanQuery) (kube.WhoCanResult, error) {
	f.gotQuery = query
	return f.result, f.err
}

// TestDebugCapabilityDeniedBlocksPanel confirms §41a's RBAC pre-check:
// "capability is checked before ↵, never after" — a denied create verb
// stops the debug panel from opening at all, surfaces the verbatim reason
// plus a 'w who-can' hint on the keybar, and queries the resource that
// matches the mode the panel would have opened in (attach, since the pod is
// running).
func TestDebugCapabilityDeniedBlocksPanel(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithContainers("default", "api-0", "app")},
	}}
	rbac := &fakeRBACChecker{result: kube.WhoCanResult{
		CurrentUser:        "dev@example.com",
		CurrentUserGranted: false,
		CurrentUserVia:     "role/viewer grants get, list on pods — not create",
	}}
	debugCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{}, // every container answers "no shell"
		RBAC:   rbac,
		OpenDebug: func(string, string, []kube.ContainerInfo, string, bool, int, int) (tea.Model, tea.Cmd) {
			debugCalled = true
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	updated, _ := m.Update(cmd())
	mm, ok := updated.(*Model)
	if !ok {
		t.Fatalf("expected browse to stay active on a denied check, got %T", updated)
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

// TestDebugCapabilityGrantedOpensPanel confirms a granted check still opens
// the panel exactly as it did before this pre-check existed.
func TestDebugCapabilityGrantedOpensPanel(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithContainers("default", "api-0", "app")},
	}}
	rbac := &fakeRBACChecker{result: kube.WhoCanResult{CurrentUser: "dev@example.com", CurrentUserGranted: true}}
	debugCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{},
		RBAC:   rbac,
		OpenDebug: func(string, string, []kube.ContainerInfo, string, bool, int, int) (tea.Model, tea.Cmd) {
			debugCalled = true
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	m.Update(cmd())
	if !debugCalled {
		t.Fatal("expected OpenDebug to be called once the capability check grants it")
	}
}

// TestDebugCapabilityCheckSkippedWithoutRBAC confirms a nil RBAC (the
// zero-value Config, same as every other test in this file) fails open —
// the panel opens unconditionally, the pre-existing behavior.
func TestDebugCapabilityCheckSkippedWithoutRBAC(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithContainers("default", "api-0", "app")},
	}}
	debugCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister,
		Shells: fakeShellDetector{},
		OpenDebug: func(string, string, []kube.ContainerInfo, string, bool, int, int) (tea.Model, tea.Cmd) {
			debugCalled = true
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	m.Update(cmd())
	if !debugCalled {
		t.Fatal("expected OpenDebug to be called when RBAC isn't wired")
	}
}

// TestDebugCapabilityDeniedWOpensWhoCan confirms the denial's 'w' handoff
// opens tasks/whocan pre-filled with the exact create/resource/namespace
// query that came back denied (docs/design v.0.11.0.dc.html §41a: "offers w
// who-can").
func TestDebugCapabilityDeniedWOpensWhoCan(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithContainers("default", "api-0", "app")},
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
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	updated, _ := m.Update(cmd())
	mm, ok := updated.(*Model)
	if !ok {
		t.Fatalf("expected browse to stay active on a denied check, got %T", updated)
	}

	next, _ := mm.Update(tea.KeyPressMsg{Text: "w"})
	if _, ok := next.(stubTask); !ok {
		t.Fatalf("expected 'w' to push tasks/whocan, got %T", next)
	}
	if gotVerb != "create" || gotResource != kube.DebugAttachResource || gotNamespace != "default" {
		t.Fatalf("openWhoCan called with (%q, %q, %q), want (create, %q, default)", gotVerb, gotResource, gotNamespace, kube.DebugAttachResource)
	}
	if mm.pendingDebugDenial != nil {
		t.Fatal("expected pendingDebugDenial to be cleared once 'w' consumes it")
	}
}
