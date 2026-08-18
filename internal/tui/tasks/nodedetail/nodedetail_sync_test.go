package nodedetail

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// notYetSyncedLister simulates *kube.Cluster's CacheSyncChecker: ListRaw for
// Pods reads a cache that's still filling (empty, no error — the same
// "truthful-looking but wrong" shape the real informer cache returns before
// WaitForCacheSync completes) until synced flips true — same shape as
// browse's own notYetSyncedLister test double, duplicated per the repo's
// package-local-seam convention. Nodes still resolve regardless of synced:
// reaching this screen at all means the caller already selected a node off
// an already-rendered Nodes list, so the Node informer, at least, is synced.
type notYetSyncedLister struct {
	lister fakeLister
	synced *bool
}

func (l *notYetSyncedLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	if !*l.synced && kind == kube.KindPod {
		return nil, nil
	}
	return l.lister.ListRaw(ctx, kind, namespace)
}

func (l *notYetSyncedLister) Synced() bool { return *l.synced }

// firstLoaded picks the loadedMsg out of a Cmd that may be a bare message or
// (as Init's is, since it also batches the spinner's tick alongside the
// load) a tea.BatchMsg — mirrors browse's own firstRowsLoaded.
func firstLoaded(t *testing.T, cmd tea.Cmd) loadedMsg {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if lm, ok := c().(loadedMsg); ok {
				return lm
			}
		}
		t.Fatal("no loadedMsg found in batch")
	}
	lm, ok := msg.(loadedMsg)
	if !ok {
		t.Fatalf("expected loadedMsg, got %T", msg)
	}
	return lm
}

// TestApplyLoadedStaysLoadingWhileCacheSyncing is the regression test for
// opening node detail on a real cluster right after launch (or right after a
// context switch) sometimes flashing "no pods on this node": an empty pod
// result from a not-yet-synced lister must not settle nodedetail at
// TaskStateReady with an empty table.
func TestApplyLoadedStaysLoadingWhileCacheSyncing(t *testing.T) {
	synced := false
	lister := &notYetSyncedLister{synced: &synced}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
	m.SetSize(120, 36)

	before := m.reloadEpoch
	updated, cmd := m.applyLoaded(loadedMsg{node: testNode("node-a"), pods: nil})
	m = *updated.(*Model)

	if m.state != tui.TaskStateLoading {
		t.Fatalf("state = %s, want loading (cache not yet synced)", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a retry command to be scheduled while the cache is still syncing")
	}
	if m.reloadEpoch == before {
		t.Fatal("expected reloadEpoch to advance so the scheduled retry is distinguishable from a stale one")
	}
}

// forbiddenPodLister simulates *kube.Cluster's Pod reflector coming back
// Forbidden: ListRaw(Pod) still returns an empty, error-free slice — the
// real informer-backed behavior — and KindSynced reports settled (the
// anti-hang rule) while KindForbidden carries the reason.
type forbiddenPodLister struct {
	lister fakeLister
	err    error
}

func (l *forbiddenPodLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	if kind == kube.KindPod {
		return nil, nil
	}
	return l.lister.ListRaw(ctx, kind, namespace)
}

func (l *forbiddenPodLister) KindSynced(kube.ResourceKind, string) bool { return true }

func (l *forbiddenPodLister) KindForbidden(kind kube.ResourceKind, _ string) error {
	if kind == kube.KindPod {
		return l.err
	}
	return nil
}

// TestDeniedPodCacheRendersPermissionDeniedNotEmpty pins the fix for §5 of
// docs/plans/namespace-scoped-final-plan.md: listerSynced (KindSynced)
// reports settled for a Forbidden cache too — that's the anti-hang rule,
// not a claim the node has zero pods — so without an explicit KindError
// check this fell straight through to TaskStateReady with an empty pods
// table instead of the permission-denied card.
func TestDeniedPodCacheRendersPermissionDeniedNotEmpty(t *testing.T) {
	lister := &forbiddenPodLister{
		lister: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
			kube.KindNode: {testNode("node-a")},
		}},
		err: apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("nope")),
	}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
	m.SetSize(120, 36)

	updated, _ := m.applyLoaded(loadedMsg{node: testNode("node-a"), pods: nil})
	m = *updated.(*Model)

	if m.state != tui.TaskStatePermissionDenied {
		t.Fatalf("state = %s, want permission-denied — the Pod cache is Forbidden, not empty", m.state)
	}
}

// unsyncedNodeLister simulates a Node cache that hasn't finished its initial
// fill: ListRaw(Node) reads empty (the same "truthful-looking but wrong"
// shape a real informer returns pre-sync — findNode reads that as "not
// found"), and KindSynced reports false only for Node.
type unsyncedNodeLister struct{}

func (unsyncedNodeLister) ListRaw(context.Context, kube.ResourceKind, string) ([]runtime.Object, error) {
	return nil, nil
}

func (unsyncedNodeLister) KindSynced(kind kube.ResourceKind, _ string) bool {
	return kind != kube.KindNode
}

// TestNodeNotYetSyncedStaysLoadingNotFalseNotFound pins the Node half of
// §5: before the fix, findNode's "node %q not found" error was treated
// identically to a real error — an unsynced Node cache (right after launch
// or mid a context switch) rendered a bare "not found" instead of staying
// loading and retrying.
func TestNodeNotYetSyncedStaysLoadingNotFalseNotFound(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: unsyncedNodeLister{}, NodeName: "node-a"})
	m.SetSize(120, 36)

	before := m.reloadEpoch
	updated, cmd := m.Update(firstLoaded(t, m.Init()))
	m = *updated.(*Model)

	if m.state != tui.TaskStateLoading {
		t.Fatalf("state = %s, want loading — the Node cache hasn't synced yet, findNode's 'not found' isn't trustworthy", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a retry command to be scheduled while the Node cache is still filling")
	}
	if m.reloadEpoch == before {
		t.Fatal("expected reloadEpoch to advance so the scheduled retry is distinguishable from a stale one")
	}
}

// forbiddenNodeLister simulates a genuinely Forbidden Node cache: ListRaw
// still reads empty and error-free (the real informer-backed behavior), and
// KindSynced reports settled (the anti-hang rule) while KindForbidden
// carries the reason.
type forbiddenNodeLister struct{}

func (forbiddenNodeLister) ListRaw(context.Context, kube.ResourceKind, string) ([]runtime.Object, error) {
	return nil, nil
}

func (forbiddenNodeLister) KindSynced(kube.ResourceKind, string) bool { return true }

func (forbiddenNodeLister) KindForbidden(kind kube.ResourceKind, _ string) error {
	if kind == kube.KindNode {
		return apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", errors.New("nope"))
	}
	return nil
}

// TestForbiddenNodeCacheRendersPermissionDeniedNotNotFound is
// TestNodeNotYetSyncedStaysLoadingNotFalseNotFound's permanent-denial half:
// there was no tui.KindsError/KindForbidden check for KindNode anywhere in
// the package, so a denied Node cache rendered a generic "not found" error
// instead of the permission-denied card.
func TestForbiddenNodeCacheRendersPermissionDeniedNotNotFound(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: forbiddenNodeLister{}, NodeName: "node-a"})
	m.SetSize(120, 36)

	updated, _ := m.Update(firstLoaded(t, m.Init()))
	m = *updated.(*Model)

	if m.state != tui.TaskStatePermissionDenied {
		t.Fatalf("state = %s, want permission-denied — the Node cache is Forbidden, not genuinely missing", m.state)
	}
}

// TestResourceChangedForNodeAndPodSchedulesReload pins the missing-reload
// half of §5: a Node condition change or a pod added/removed on this node
// never triggered a reload, because nodedetail had no
// kube.ResourceChangedMsg handling at all — mirrors browse's own
// TestAuxKindsReloadTheList.
func TestResourceChangedForNodeAndPodSchedulesReload(t *testing.T) {
	for _, kind := range []kube.ResourceKind{kube.KindNode, kube.KindPod} {
		m := New(Config{Session: newSession(), Lister: fakeLister{}, NodeName: "node-a"})
		m.SetSize(120, 36)

		before := m.reloadEpoch
		updated, cmd := m.Update(kube.ResourceChangedMsg{Kind: kind})
		m = *updated.(*Model)
		if cmd == nil || m.reloadEpoch == before {
			t.Fatalf("a %s change must reload node detail — it backs the facts panel/pods table", kind)
		}
	}
}

func TestResourceChangedForUnrelatedKindDoesNotReload(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}, NodeName: "node-a"})
	m.SetSize(120, 36)

	before := m.reloadEpoch
	updated, cmd := m.Update(kube.ResourceChangedMsg{Kind: kube.KindConfigMap})
	m = *updated.(*Model)
	if cmd != nil || m.reloadEpoch != before {
		t.Fatal("a ConfigMap change has nothing to do with node detail")
	}
}

// TestNodeDetailStaysLoadingUntilCacheSynced drives the retry through to
// completion: once the lister reports synced, nodedetail settles at Ready
// with the real pods rather than getting stuck showing an empty table. The
// synced flag flips between the first (empty) load and the scheduled retry,
// same as the real Cluster's WaitForCacheSync completing in the background
// while this screen is already showing its loading state.
func TestNodeDetailStaysLoadingUntilCacheSynced(t *testing.T) {
	synced := false
	lister := &notYetSyncedLister{synced: &synced, lister: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
		kube.KindPod:  {schedPod("default", "big", "node-a", "2Gi")},
	}}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
	m.SetSize(120, 36)

	updated, cmd := m.Update(firstLoaded(t, m.Init()))
	m = *updated.(*Model)
	if m.state != tui.TaskStateLoading {
		t.Fatalf("state = %s, want loading right after the first (unsynced) empty load", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a retry command to be scheduled")
	}

	synced = true // the cache finishes syncing while the retry is pending
	updated, cmd = m.Update(cmd())
	m = *updated.(*Model)
	if cmd == nil {
		t.Fatal("expected the retry to re-issue a load")
	}

	updated, _ = m.Update(cmd())
	m = *updated.(*Model)

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready once the cache reports synced (feedback=%q)", m.state, m.feedback)
	}
	if !strings.Contains(plain(m.Render()), "big") {
		t.Fatalf("expected the table to show the pod once synced:\n%s", plain(m.Render()))
	}
}
