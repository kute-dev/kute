package browse

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// firstEmptyHints picks the emptyHintsMsg out of a bare Cmd — unlike
// firstRowsLoaded's rowsLoadedMsg, loadEmptyHints is never batched with
// anything else, so there's no fan-out to unwrap.
func firstEmptyHints(t *testing.T, cmd tea.Cmd) emptyHintsMsg {
	t.Helper()
	msg, ok := cmd().(emptyHintsMsg)
	if !ok {
		t.Fatalf("expected emptyHintsMsg, got %T", msg)
	}
	return msg
}

// TestStaleEmptyHintsDoNotOverwriteNewNamespace pins TODO.md item 4: kind
// alone doesn't catch a namespace switch that lands back on the same kind —
// two empty namespaces of the same kind both leave m.state ==
// TaskStateEmpty and msg.kind unchanged, so a slow hints reply for the
// namespace just left could still land after the switch's own faster reply
// already populated m.hints for the new namespace. The guard has to be
// epoch/namespace, the same fix rowsLoadedMsg already has (see
// TestStaleNamespaceLoadDoesNotOverwriteNewNamespace).
func TestStaleEmptyHintsDoNotOverwriteNewNamespace(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		// Pods: empty in both "default" and "nva-stage", so both namespace
		// switches land on TaskStateEmpty directly. One configmap lives in
		// "default" only, so its hints are observably different from
		// nva-stage's (otherwise-identical) empty hints.
		kube.KindConfigMap: {configMap("default", "cfg-1")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	if m.namespace != "default" {
		t.Fatalf("namespace = %q, want default", m.namespace)
	}
	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty", m.state)
	}

	// Capture "default"'s in-flight hints load before it resolves — this is
	// the request that's about to be superseded.
	staleCmd := m.loadEmptyHints()

	// The user switches to nva-stage (also empty for Pods, no configmaps)
	// before that load lands; this bumps reloadEpoch and issues its own
	// fresh hints load, which resolves within this same step since
	// nva-stage has nothing to hint about either.
	m = step(t, m, tui.SwitchNamespaceMsg{Namespace: "nva-stage"})
	if m.namespace != "nva-stage" {
		t.Fatalf("namespace = %q, want nva-stage", m.namespace)
	}
	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty", m.state)
	}
	if len(m.hints.otherKinds) != 0 {
		t.Fatalf("nva-stage's fresh hints should be empty, got %+v", m.hints)
	}

	// The stale "default" hints reply now resolves late. Without the
	// epoch/namespace guard this overwrites m.hints with default's configmap
	// hint under nva-stage's breadcrumb.
	staleMsg := firstEmptyHints(t, staleCmd)
	if staleMsg.namespace != "default" {
		t.Fatalf("staleMsg.namespace = %q, want default", staleMsg.namespace)
	}
	if len(staleMsg.hints.otherKinds) == 0 {
		t.Fatal("stale message must carry a real hint to make the guard's effect observable")
	}
	updated, cmd := m.Update(staleMsg)
	m = *updated.(*Model)
	if cmd != nil {
		t.Fatal("a stale hints reply must not schedule anything either")
	}

	if len(m.hints.otherKinds) != 0 {
		t.Fatalf("stale default-namespace hints leaked into nva-stage: %+v", m.hints)
	}
}
