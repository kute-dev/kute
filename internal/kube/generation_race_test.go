package kube

import (
	"errors"
	"sync"
	"testing"
)

// TestRecordWatchErrorNeverLeaksIntoReplacementContextsHealth is the real-
// interleaving counterpart to TestGenerationGuardDropsStaleCallbacks and
// TestNoteWatchErrorDropsStaleGeneration/TestMarkKindFailedDropsStaleGeneration
// (docs/TODO.md's "Finish context-generation isolation"). Those pin the
// generation check itself by manually incrementing c.generation between two
// sequential calls — useful, but it can't catch a race between the check and
// something that happens *after* it, which is exactly the shape the bug had:
// the old two-call version re-checked the generation atomically with the
// kindFailed/kindStalled write (noteWatchError/markKindFailed), but then
// called health.onWatchError afterward, unlocked relative to c.mu — so a
// SwitchContext-shaped critical section (generation bump + health.reset(),
// both under c.mu, exactly as SwitchContext itself does) could run in the
// gap between the write and the health call, and the health call would then
// flip the freshly-reset replacement context's health back to Reconnecting
// using the old context's error.
//
// This drives the real production entry point (recordWatchError) against a
// real concurrent goroutine performing that exact critical section, with no
// manual sequencing beyond a shared start gate — the Go scheduler decides
// the actual interleaving each iteration, and go test -race can catch any
// access that isn't actually protected by c.mu/h.mu. recordWatchError holds
// c.mu across the generation check, the state write, and the
// health.onWatchError call, so no interleaving of the two goroutines can
// produce anything but a clean Connected state once both finish.
func TestRecordWatchErrorNeverLeaksIntoReplacementContextsHealth(t *testing.T) {
	t.Parallel()
	c := newSyncTestCluster()
	defer close(c.stopCh)

	staleErr := errors.New("stale-context watch error")

	for i := 0; i < 200; i++ {
		c.mu.Lock()
		gen := c.generation
		c.mu.Unlock()

		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)

		// Exactly what a stale reflector callback does in production now —
		// registerTypedWatchLocked, ensureDynamicKind, and
		// ensureHelmSecrets' watch-error handlers are all a one-line call
		// to this.
		go func() {
			defer wg.Done()
			<-start
			c.recordWatchError(gen, KindSecret, "", staleErr)
		}()

		// Exactly what SwitchContext's own critical section does: bump the
		// generation and reset health, both under one c.mu hold.
		go func() {
			defer wg.Done()
			<-start
			c.mu.Lock()
			c.generation++
			c.health.reset()
			c.mu.Unlock()
		}()

		close(start)
		wg.Wait()

		if got := c.health.get(); got.Phase != ConnConnected || got.Err != "" {
			t.Fatalf("iteration %d: health = %+v after a concurrent switch; a stale watch error must never survive a context replacement", i, got)
		}
	}
}
