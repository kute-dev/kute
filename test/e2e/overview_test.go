//go:build e2e

package e2e

import (
	"testing"
)

// TestOverviewPanelsAndRouting covers §19a beyond the no-metrics-server row
// metrics_test.go already owns.
//
// The overview is "a routing layer, not a dashboard", and everything on it
// is a cluster-wide read the rest of the app never makes: NODES reads every
// node, TROUBLE is a cluster-wide unhealthy-pod scan, and RECENT CHANGES is
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

	// NODES folds to a one-line all-clear when nothing is wrong with any
	// node — the panel lists *troubled* nodes, not every node — so the
	// healthy reading is that summary. Not pinned to a node count: the kind
	// config's node total is not what this test is about, and hard-coding it
	// would break the suite on a differently shaped cluster.
	a.WaitFor("nodes ready", Settle)

	// TROUBLE: the worker fixture crash-loops permanently, so a cluster-wide
	// unhealthy scan has to find it. Asserted by pod-name prefix rather than
	// by its reason — CrashLoopBackOff is only true between restarts, and
	// the harness's third rule is to never assert on transient state.
	a.WaitFor("worker-", Settle)

	// The routing claim: ↵ on the focused panel opens the object it names.
	// ↹ moves focus, skipping any panel with nothing selectable — which on a
	// healthy cluster is NODES — so one press lands on TROUBLE.
	//
	// The fence is the destination's keybar pill, not the word "Pods": the
	// overview's own CAPACITY bar is labelled "pods" and its TROUBLE rows
	// name the namespace, so only the pill ("PODS", where the overview's is
	// "OVERVIEW") says the navigation actually landed.
	a.Press("tab")
	a.Enter()
	a.WaitFor("PODS", Settle)
	a.WaitFor("worker-", Settle)
}
