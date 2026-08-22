package tui

import (
	"context"
	"sync"
)

// This file gives Session the two contexts every cluster read hangs off, so
// that a read started by a screen can actually be cancelled by the two events
// that make its answer worthless: the program quitting, and the active
// kube-context being swapped underneath it.
//
// The process-lifetime context is built at the composition root (app.run) —
// it is the same ctx that stops forwardEvents and the informer factories, so
// hanging screen reads off it means "quit" means one thing everywhere rather
// than "the informers stop, and whatever a screen had in flight against the
// API server keeps going until its own timeout expires".
//
// Reads, not writes. Every mutating verb still executes on
// context.Background() (actions.Controller): cancelling a write mid-flight
// doesn't un-apply it — the API server may well have committed it — so
// abandoning the response would turn a successful delete into a reported
// failure. A read has no such asymmetry; dropping it loses nothing.
//
// Two levels rather than one, because the two events have different scopes:
//
//   - Context() is the process context. Use it for work that isn't bound to
//     the cluster currently being browsed — probing every kubeconfig context
//     (7a), the update check, the context switch itself. Only quit cancels it.
//   - ClusterContext() is a child of it, replaced on every context switch.
//     Use it for anything read *through* the active cluster. Both quit and a
//     switch cancel it, which is what keeps a slow list against the cluster
//     you just left from landing in a screen now showing a different one.
//
// Both are nil-receiver safe and fall back to context.Background(), so a
// Session built in a test (or before app.run has installed anything) behaves
// exactly as it did before this existed.

// sessionCtx is Session's context state, guarded by its own mutex for the
// same reason countCache is: a tea.Cmd goroutine can be reading the parent it
// was handed while the Update loop resets it on a context switch, and
// app.attemptReconnect/attemptSwitchContext reset it from a Cmd goroutine
// outright.
type sessionCtx struct {
	mu      sync.Mutex
	root    context.Context
	cluster context.Context
	cancel  context.CancelFunc
}

// SetContext installs the process-lifetime context — called once, from the
// composition root, with the same ctx that bounds the event-forwarding
// goroutines and the informer factories. Any cluster context derived from a
// previous root is cancelled: a root only ever changes when the whole program
// context does.
func (s *Session) SetContext(ctx context.Context) {
	if s == nil || ctx == nil {
		return
	}
	s.ctx.mu.Lock()
	defer s.ctx.mu.Unlock()
	s.ctx.cancelClusterLocked()
	s.ctx.root = ctx
}

// Context is the process-lifetime context: cancelled when the program exits,
// and by nothing else. context.Background() until the composition root has
// installed one (tests, and the window before app.run reaches it).
func (s *Session) Context() context.Context {
	if s == nil {
		return context.Background()
	}
	s.ctx.mu.Lock()
	defer s.ctx.mu.Unlock()
	return s.ctx.rootLocked()
}

// ClusterContext is the parent for every read made through the active
// cluster — cancelled on quit *and* on a context switch (ResetClusterContext).
// Screens capture it when they build a tea.Cmd, the same place they capture
// every other Session value, and derive their own per-read timeout from it.
func (s *Session) ClusterContext() context.Context {
	if s == nil {
		return context.Background()
	}
	s.ctx.mu.Lock()
	defer s.ctx.mu.Unlock()
	if s.ctx.cluster == nil {
		s.ctx.cluster, s.ctx.cancel = context.WithCancel(s.ctx.rootLocked())
	}
	return s.ctx.cluster
}

// ResetClusterContext cancels every read still in flight against the cluster
// being left and arms a fresh context for the one being entered — the context
// counterpart of InvalidateCounts, and called from the same places for the
// same reason: those results describe a cluster the user is no longer on.
// Call it *before* the swap starts, not after it lands: the rebuild itself
// blocks for as long as the new cluster's caches take to sync, and that is
// precisely the window in which the stale reads would otherwise complete.
func (s *Session) ResetClusterContext() {
	if s == nil {
		return
	}
	s.ctx.mu.Lock()
	defer s.ctx.mu.Unlock()
	s.ctx.cancelClusterLocked()
}

func (c *sessionCtx) rootLocked() context.Context {
	if c.root == nil {
		return context.Background()
	}
	return c.root
}

// cancelClusterLocked drops the current cluster context; the next
// ClusterContext call builds a fresh one off whatever root is installed then.
func (c *sessionCtx) cancelClusterLocked() {
	if c.cancel != nil {
		c.cancel()
	}
	c.cluster, c.cancel = nil, nil
}
