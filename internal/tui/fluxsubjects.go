package tui

import (
	"maps"
	"sync"
)

// This file is §32a's commit-subject cache (docs/flux-plan.md T14). The
// subject and the row it belongs to have different lifetimes on the cluster:
// a Kustomization re-emits its reconcile event — and with it the revision
// annotation — every interval, so a "◆ revision applied" row for the
// currently-applied revision is effectively permanent, while
// source-controller's "stored artifact for commit '<subject>'" event fires
// once per commit and ages out at the ~1h Event TTL. Without retention the
// failure isn't "no subject": the subject shows for about an hour and then
// disappears from a row that stays on screen, silently losing information
// the app already had.
//
// Deliberately session-scoped and in memory, not persisted. Informers LIST
// on startup, so kute picks up events emitted before it launched — but only
// inside that same TTL. Open kute cold two days after a commit and no cache
// can invent the subject; the cache only retains what was already seen, it
// adds no capability. Surviving a restart would cost versioned persisted
// state plus a migration (the persisted-state invariant) for a case the TTL
// rules out anyway.
//
// Not cleared on a context switch, unlike countCache next door: a count is a
// statement about one cluster, but a commit subject is a fact about a git
// commit, keyed by the revision (`master@sha1:efd398be…`) that names it.
// Two contexts tracking the same repo agree on it by construction, and a
// content-addressed SHA can't collide with a different message.

// fluxSubjectCache retains revision→commit-subject pairs for the life of the
// session. Guarded by its own mutex because timeline's load() merges into it
// from a tea.Cmd goroutine while the Update loop may read — the same reason
// countCache carries one.
type fluxSubjectCache struct {
	mu       sync.Mutex
	subjects map[string]string
}

// merge folds seen into the cache and returns a snapshot of everything known
// afterwards. Merged rather than replaced: outliving the events that carried
// them is the whole point.
func (c *fluxSubjectCache) merge(seen map[string]string) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subjects == nil {
		c.subjects = make(map[string]string, len(seen))
	}
	for rev, subject := range seen {
		if rev == "" || subject == "" {
			continue
		}
		c.subjects[rev] = subject
	}
	out := make(map[string]string, len(c.subjects))
	maps.Copy(out, c.subjects)
	return out
}

// RetainFluxCommitSubjects merges one load's freshly observed
// revision→subject pairs into the session cache and returns every pair known
// afterwards, for §32a's revision rows to read. Safe to call off the Update
// loop; safe on a nil Session (returns seen unchanged), which is what a
// screen constructed without one gets.
//
// A cached entry can never be a SHA masquerading as a commit message:
// kube.FluxCommitSubject rejects a revision-shaped capture before it ever
// reaches here (source-controller substitutes the revision into the same
// message when the commit had no subject).
func (s *Session) RetainFluxCommitSubjects(seen map[string]string) map[string]string {
	if s == nil {
		return seen
	}
	return s.fluxSubjects.merge(seen)
}
