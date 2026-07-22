package nodedetail

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

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
