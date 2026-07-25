package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

// KindSyncChecker is implemented by a lister whose per-kind caches populate
// asynchronously (*kube.Cluster's informers, which start on first read).
// Mirrors browse.KindSyncChecker; declared here too so task packages that
// don't import browse can gate on it.
type KindSyncChecker interface {
	KindSynced(kind kube.ResourceKind) bool
}

// CacheSyncChecker is the pre-per-kind, cluster-wide form.
type CacheSyncChecker interface {
	Synced() bool
}

// KindsSynced reports whether every one of kinds has a cache worth believing.
//
// This is what stands between a screen and the worst failure mode of lazy
// informers: reading a kind for the first time returns an empty cache, and a
// screen that takes that at face value announces "nothing here" about data
// that is seconds away. The empty *state* is a claim about the cluster, so it
// may only be entered once the caches backing it have actually filled.
//
// True for any lister implementing neither checker (fakes, test doubles), so
// this only changes behavior against a live cluster. Also true for kinds
// nothing will ever deliver — a stopped cluster, a kind with no informer, one
// whose watch came back Forbidden — so a caller gating a spinner on it cannot
// hang waiting for a cache that is never coming.
func KindsSynced(lister any, kinds ...kube.ResourceKind) bool {
	if lister == nil {
		return true
	}
	if kc, ok := lister.(KindSyncChecker); ok {
		for _, kind := range kinds {
			if !kc.KindSynced(kind) {
				return false
			}
		}
		return true
	}
	if sc, ok := lister.(CacheSyncChecker); ok {
		return sc.Synced()
	}
	return true
}

// CacheSyncRetryInterval is how often a screen re-checks a cache that was
// still filling when it last looked. Matches browse's own reload debounce.
//
// A retry is needed rather than relying on the informer's change events
// alone, because a kind with genuinely zero objects syncs without ever
// emitting one — so the screen would otherwise sit on a spinner forever for
// the one case where "empty" is the right answer.
const CacheSyncRetryInterval = 250 * time.Millisecond

// CacheSyncRetryMsg asks a screen to reload because a cache it read was not
// yet populated. Screens carry their own reload epoch in Gen and drop a msg
// that doesn't match, so a retry scheduled before a since-superseded load
// can't resurrect it.
type CacheSyncRetryMsg struct{ Gen int }

// ScheduleCacheSyncRetry is the tick behind CacheSyncRetryMsg.
func ScheduleCacheSyncRetry(gen int) tea.Cmd {
	return tea.Tick(CacheSyncRetryInterval, func(time.Time) tea.Msg {
		return CacheSyncRetryMsg{Gen: gen}
	})
}
