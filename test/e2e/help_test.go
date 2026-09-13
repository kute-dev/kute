//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestHelpOverlayRendersTheActiveScreensKeys covers §7b, which nothing in
// this suite pressed.
//
// The overlay is the one surface with no data of its own: every row comes
// from the *active* screen's Keybar plus the session's fixed SCOPE/LIST/
// RESOURCE columns, which the composition root builds from the verb
// registry. That makes it the screen a registry or keybar change breaks
// while every other screen still renders correctly — and it is only
// assertable through the real root shell, since the overlay is composed
// above the task stack rather than by any task.
func TestHelpOverlayRendersTheActiveScreensKeys(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)

	a.Press("?")

	// The title names the view it was opened over, and the fixed columns are
	// the ones the composition root supplies.
	a.WaitForAll(Settle,
		"globals below",
		"keys for PODS view",
		"PODS VIEW",
		"SCOPE",
		"LIST",
		"RESOURCE",
		"MISC",
	)
	// A handful of keys that have to be *taught* somewhere, because they are
	// deliberately absent from the keybar: the palettes and all-namespaces
	// live only here.
	a.WaitForAll(Settle, "all namespaces", "namespace", "context")
	a.WaitFor("esc close", Settle)

	// esc closes it and returns to the same screen, which is still live
	// underneath rather than having been popped. Closed is asserted on the
	// overlay's own title line, never on "? help":
	// that string is also the keybar's permanent right-hand hint, so it is on
	// screen whether the overlay is open or not.
	a.Esc()
	a.WaitGone("globals below", Settle)
	a.WaitFor("api-", Settle)

	// The load-bearing half: the VIEW column follows the active screen, so
	// the same key over a pushed task documents that task instead. Pod
	// detail's pill is POD, and its own keys are not the list's.
	a.selectAPIPod(t)
	a.Enter()
	a.WaitFor("CONTAINERS", Settle)
	a.Press("?")
	a.WaitForAll(Settle, "globals below", "keys for POD view", "POD VIEW")
	// And the list's own view heading is gone — a stale overlay showing the
	// screen underneath the stack is exactly the bug this pins.
	a.Never("PODS VIEW", 2*time.Second)

	a.Esc()
	a.WaitGone("globals below", Settle)
	a.WaitFor("CONTAINERS", Settle)
}
