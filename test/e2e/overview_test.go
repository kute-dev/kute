//go:build e2e

package e2e

import (
	"testing"
)

// TestOverviewPanelsAndRouting covers §19a beyond the no-metrics-server row
// metrics_test.go already owns.
//
// The overview is "a routing layer, not a dashboard", and everything on it
// is a cluster-wide read the rest of the app never makes: NODES lists real
// nodes, TROUBLE is a cluster-wide unhealthy-pod scan, and RECENT CHANGES is
// derived from ReplicaSets rather than from any list the user opened. A fake
// can render all four panels; only a real cluster can put the crash-looping
// worker in the one that is supposed to find it without being asked.
func TestOverviewPanelsAndRouting(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)

	// Reached only through the goto palette — the overview is not a resting
	// screen and nothing navigates to it by default.
	a.gotoPalette(t, "overview", "cluster · routing", "Cluster Overview")
	a.WaitLoaded(Settle)

	// All four panels, by their own section titles.
	a.WaitForAll(Settle, "CAPACITY", "NODES", "TROUBLE", "RECENT CHANGES")

	// NODES: the kind cluster's own nodes, read cluster-wide.
	node := NodeNamePrefix(t) + "-control-plane"
	a.WaitFor(node, Settle)

	// TROUBLE: the worker fixture crash-loops permanently, so a cluster-wide
	// unhealthy scan has to find it. Asserted by pod-name prefix rather than
	// by its reason — CrashLoopBackOff is only true between restarts, and
	// the harness's third rule is to never assert on transient state.
	a.WaitFor("worker-", Settle)

	// The routing claim: ↵ on the focused panel opens the object it names.
	// NODES is focused on open, and node detail is a pushed screen with its
	// own CONDITIONS section that the overview itself never renders — so it
	// is an honest fence for the navigation having landed.
	a.Enter()
	a.WaitLoaded(Settle)
	a.WaitForAll(Settle, node, "CONDITIONS")

	// esc walks back exactly one level, to the overview rather than to the
	// list the palette was opened from.
	a.Esc()
	a.WaitFor("Cluster Overview", Settle)

	// ↹ moves to TROUBLE, and ↵ there jumps to the object through the same
	// goto navigation the palette uses — landing on the Pods list with the
	// unhealthy pod selected, not on a bespoke detail screen.
	a.Press("tab")
	a.Enter()
	// The keybar pill, not the word "Pods": the overview's own CAPACITY bar
	// is labelled "pods" and its TROUBLE rows name the kind, so only the
	// destination's pill ("PODS", where the overview's is "OVERVIEW") says
	// the navigation actually landed.
	a.WaitFor("PODS", Settle)
	a.WaitFor("worker-", Settle)
}
