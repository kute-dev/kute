package tui_test

import (
	"context"
	"sync"
	"testing"

	"github.com/kute-dev/kute/internal/tui"
)

// A Session nobody has handed a context to still answers with a usable,
// never-cancelled one — that's what keeps every test and the window before
// app.run installs the real root behaving exactly as they did before.
func TestSessionContextDefaultsToBackground(t *testing.T) {
	var nilSess *tui.Session
	for name, got := range map[string]context.Context{
		"nil session root":     nilSess.Context(),
		"nil session cluster":  nilSess.ClusterContext(),
		"zero session root":    (&tui.Session{}).Context(),
		"zero session cluster": (&tui.Session{}).ClusterContext(),
	} {
		if got == nil {
			t.Errorf("%s: got nil context", name)
			continue
		}
		if got.Done() != nil {
			select {
			case <-got.Done():
				t.Errorf("%s: context already cancelled", name)
			default:
			}
		}
	}
	// Resetting one that was never handed out must not panic either.
	nilSess.ResetClusterContext()
	(&tui.Session{}).ResetClusterContext()
}

// Quitting cancels reads: the root the composition root installs is the
// parent of every cluster read, so cancelling it (app.run's deferred cancel)
// takes the in-flight ones with it.
func TestSessionClusterContextCancelledWithRoot(t *testing.T) {
	root, cancel := context.WithCancel(t.Context())
	sess := &tui.Session{}
	sess.SetContext(root)

	cluster := sess.ClusterContext()
	select {
	case <-cluster.Done():
		t.Fatal("cluster context cancelled before the root was")
	default:
	}

	cancel()
	<-cluster.Done()
	if err := sess.Context().Err(); err == nil {
		t.Fatal("root context reports no error after cancel")
	}
}

// Switching context cancels the reads belonging to the cluster being left,
// and the next read gets a live context — not the cancelled one.
func TestResetClusterContextCancelsOnlyTheCluster(t *testing.T) {
	sess := &tui.Session{}
	sess.SetContext(t.Context())

	before := sess.ClusterContext()
	sess.ResetClusterContext()

	if before.Err() == nil {
		t.Fatal("reads against the cluster being left were not cancelled")
	}
	if err := sess.Context().Err(); err != nil {
		t.Fatalf("process context cancelled by a context switch: %v", err)
	}
	after := sess.ClusterContext()
	if after.Err() != nil {
		t.Fatal("the context handed to the next read is already cancelled")
	}
	if after == before {
		t.Fatal("ClusterContext handed back the cancelled context")
	}
}

// Installing a new root leaves nothing hanging off the old one.
func TestSetContextCancelsPreviousClusterContext(t *testing.T) {
	sess := &tui.Session{}
	sess.SetContext(t.Context())
	stale := sess.ClusterContext()

	second, cancelSecond := context.WithCancel(t.Context())
	defer cancelSecond()
	sess.SetContext(second)

	if stale.Err() == nil {
		t.Fatal("cluster context derived from the previous root is still live")
	}
	cancelSecond()
	<-sess.ClusterContext().Done()
}

// A tea.Cmd goroutine reads the parent it was handed while the Update loop
// (or app.attemptReconnect, from a goroutine of its own) resets it — run
// under -race, this is the reason sessionCtx carries a mutex.
func TestSessionContextConcurrentAccess(t *testing.T) {
	sess := &tui.Session{}
	sess.SetContext(t.Context())

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				_ = sess.ClusterContext()
				_ = sess.Context()
			}
		})
		wg.Go(func() {
			for range 100 {
				sess.ResetClusterContext()
			}
		})
	}
	wg.Wait()
}
