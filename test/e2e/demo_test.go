//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestDemoModeRunsWithoutACluster launches --demo.
//
// It is the other branch of app.NewModel — kube/fake behind the same seams —
// and nothing exercised it end to end, even though it is what every
// screenshot, recording and first run without a kubeconfig shows. The fake
// is required by invariant to stay feature-complete, and the way that decays
// is silently: a screen gains a seam the real cluster implements and the
// fake does not, and only a launch notices.
//
// Deliberately not RequireCluster: needing a cluster to prove the clusterless
// mode works would be its own bug. It stays in this suite rather than in a
// task package's unit tests because what is under test is the composition
// root's second wiring, not any one screen.
func TestDemoModeRunsWithoutACluster(t *testing.T) {
	a := Launch(t, WithDemo())

	// The fake's own fixtures, on the resting screen: its context, its
	// namespace, and a pod that exists nowhere else.
	a.WaitForAll(Connect, "demo", "default", "api-7d9f6c8-abcde")
	a.WaitLoaded(Settle)

	// No cluster was reachable and none was needed — the offline pill and
	// the unreachable card are both states the demo must never enter.
	a.Never("unreachable", 3*time.Second)
	a.Never("OFFLINE", 2*time.Second)

	// The screens most likely to be wired only for the real cluster: a
	// pushed detail, and a kind the fake has to serve through the same lazy
	// ListRaw seam.
	a.selectRow(t, "api-7d9f6c8-abcde")
	a.Enter()
	a.WaitLoaded(Settle)
	a.WaitFor("CONTAINERS", Settle)
	a.Esc()
	a.WaitFor("PODS", Settle)

	a.gotoKind(t, "deployments", "Deployments")
	a.WaitLoaded(Settle)
	a.WaitFor("api", Settle)
}
