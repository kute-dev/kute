//go:build e2e && e2e_scale

// The scale row of the suite, behind its own tag on top of e2e so it never
// runs in the PR job: it needs the kwok cluster scripts/e2e-scale-cluster.sh
// builds, and it takes minutes rather than seconds.
//
//	scripts/e2e-scale-cluster.sh up
//	go test -tags "e2e e2e_scale" -count=1 -timeout 30m ./test/e2e/...
//
// What it measures only exists at size. Connect fills three informer caches;
// against 5,000 pods that is the moment the "the app pays for the screen
// you're on" rule either holds or doesn't, and the number that shows whether
// it held is how much heap those three caches occupy — not how the frame
// looks.
package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const (
	// connectBudget is what "kute opens" has to mean on a 5k-pod cluster.
	// Generous on purpose: this is a regression guard against connect
	// growing an extra informer's worth of LIST, not a benchmark.
	connectBudget = 30 * time.Second

	// heapBudget is what the three eager caches may cost. Pods dominate:
	// managedFields are stripped on the way in (transform.go), which is most
	// of why this number is a few tens of MB rather than hundreds.
	heapBudget = 400 << 20 // 400 MiB
)

// scaleKubeconfigPath is where scripts/e2e-scale-cluster.sh writes.
func scaleKubeconfigPath() string {
	if p := os.Getenv("KUTE_E2E_SCALE_KUBECONFIG"); p != "" {
		return p
	}
	return filepath.Join(repoRoot(), ".kube", "e2e-scale.config")
}

// requireScaleCluster skips the test unless the kwok scale kubeconfig exists.
// The nightly scale job deliberately provisions no ordinary e2e cluster.
func requireScaleCluster(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(scaleKubeconfigPath()); err != nil {
		t.Skipf("no scale cluster: %v\nrun: scripts/e2e-scale-cluster.sh up", err)
	}
}

// TestScaleConnectAndFirstFrame: on a cluster with 5,000 pods, kute reaches a
// rendered list inside the connect budget, and the frame it reaches is a real
// one — the pod count in the header, not a spinner that has given up.
func TestScaleConnectAndFirstFrame(t *testing.T) {
	requireScaleCluster(t)
	path := scaleKubeconfigPath()

	start := time.Now()
	a := Launch(t, WithKubeconfig(path), WithNamespace("scale-00"))

	// A row from the fixture workload, which only appears once the Pod cache
	// has actually filled.
	a.WaitFor("web-", connectBudget)
	elapsed := time.Since(start)
	t.Logf("connect to first populated frame: %s", elapsed)
	if elapsed > connectBudget {
		t.Errorf("connect took %s, budget %s", elapsed, connectBudget)
	}

	// The list is real, not a truncated placeholder: the header carries the
	// count and the table has paged rather than tried to render 5,000 rows.
	a.WaitLoaded(Settle)
	a.WaitFor("pods", Settle)

	// Heap after the eager caches have filled. Read after a GC so this is
	// live data rather than whatever the allocator has not yet swept.
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	t.Logf("heap in use after connect: %.1f MiB", float64(stats.HeapAlloc)/(1<<20))
	if stats.HeapAlloc > heapBudget {
		t.Errorf("heap after connect is %.1f MiB, budget %d MiB", float64(stats.HeapAlloc)/(1<<20), heapBudget>>20)
	}
}

// TestScaleNavigationStaysResponsive: the rule this exists for is that
// browsing a big cluster must not start an informer per kind. Walking the
// goto palette across a dozen kinds on a 5k-pod cluster is exactly the
// sequence that once hung the app for a minute.
func TestScaleNavigationStaysResponsive(t *testing.T) {
	requireScaleCluster(t)
	path := scaleKubeconfigPath()

	a := Launch(t, WithKubeconfig(path), WithNamespace("scale-00"))
	a.WaitFor("web-", connectBudget)

	// Opening the palette reads a count per kind, and those counts come from
	// CountLive rather than from informer caches. If that ever regressed,
	// this is where it would show: as a palette that takes a minute to open.
	start := time.Now()
	a.Press("g")
	a.WaitFor("jump anywhere", Settle)
	t.Logf("goto palette open: %s", time.Since(start))
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the goto palette took %s to open — it is counting kinds through their informers", elapsed)
	}
	a.Esc()

	// And a screen that is not the resting one still lands promptly.
	start = time.Now()
	a.gotoKind(t, "deployments", "Deployments")
	a.WaitLoaded(Settle)
	t.Logf("switch to Deployments: %s", time.Since(start))
}
