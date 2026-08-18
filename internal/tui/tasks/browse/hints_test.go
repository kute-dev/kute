package browse

import (
	"context"
	"sync"
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

// scopedRecordingLister wraps fakeLister with tui.ScopedChecker and
// tui.LiveCounter — the shape *kube.Cluster's decorator stack presents under
// --namespace-scoped, where CountLive answers server-side without touching a
// cache — and records every ListRaw call so a test can see whether one
// asked for a kind cluster-wide.
type scopedRecordingLister struct {
	fakeLister
	mu        sync.Mutex
	listCalls []scopedListCall
	liveCount int
}

type scopedListCall struct {
	kind      kube.ResourceKind
	namespace string
}

func (l *scopedRecordingLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	l.mu.Lock()
	l.listCalls = append(l.listCalls, scopedListCall{kind, namespace})
	l.mu.Unlock()
	return l.fakeLister.ListRaw(ctx, kind, namespace)
}

func (l *scopedRecordingLister) Scoped() bool { return true }

func (l *scopedRecordingLister) CountLive(_ context.Context, _ kube.ResourceKind, _ string) (int, error) {
	return l.liveCount, nil
}

func (l *scopedRecordingLister) reset() {
	l.mu.Lock()
	l.listCalls = nil
	l.mu.Unlock()
}

func (l *scopedRecordingLister) globalReads() []kube.ResourceKind {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []kube.ResourceKind
	for _, c := range l.listCalls {
		if c.namespace == "" {
			out = append(out, c.kind)
		}
	}
	return out
}

// TestScopedEmptyHintsSkipGlobalRead pins TODO.md item 5: under
// --namespace-scoped, resources.Count(kind, "") for the "N cluster-wide"
// hint detail goes through ListRaw and would start a brand-new cluster-wide
// informer for kind just to decorate an empty state — the exact
// breadth-first, implicit global read scoped mode exists to avoid.
// loadEmptyHints must answer the same question from CountLive instead, with
// no namespace=="" ListRaw call for any kind.
func TestScopedEmptyHintsSkipGlobalRead(t *testing.T) {
	lister := &scopedRecordingLister{
		fakeLister: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}},
		liveCount:  5,
	}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty", m.state)
	}
	// Only loadEmptyHints's own reads matter here; the initial load already
	// issued its own (legitimate, cluster-scoped) Node count for the health
	// strip.
	lister.reset()

	msg := firstEmptyHints(t, m.loadEmptyHints())

	if reads := lister.globalReads(); len(reads) != 0 {
		t.Fatalf("loadEmptyHints issued a cluster-wide ListRaw for %v under --namespace-scoped; CountLive should have answered instead", reads)
	}
	if msg.hints.allCount != 5 {
		t.Fatalf("hints.allCount = %d, want 5 from CountLive", msg.hints.allCount)
	}
}
