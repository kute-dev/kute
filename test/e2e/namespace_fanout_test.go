//go:build e2e && e2e_soak

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestNamespaceFanOutRetainsOneCachePerReadNamespace pins the chosen policy:
// namespace-scoped caches are retained without an LRU for the life of one
// cluster context, then all are released by context switch or shutdown.
// Growth is intentionally linear, so the budgets are per visited namespace.
func TestNamespaceFanOutRetainsOneCachePerReadNamespace(t *testing.T) {
	count := soakCount(t, "KUTE_E2E_SOAK_NAMESPACES", 24)
	run := soakName("fanout")
	client := e2eClientset(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*Settle)
	defer cancel()

	namespaces := make([]string, count)
	markers := make([]string, count)
	for i := range count {
		namespaces[i] = fmt.Sprintf("%s-%02d", run, i)
		markers[i] = fmt.Sprintf("fanout-marker-%02d", i)
		if _, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaces[i], Labels: map[string]string{"kute.dev/soak-run": run}}}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("creating namespace %s: %v", namespaces[i], err)
		}
		if _, err := client.CoreV1().ConfigMaps(namespaces[i]).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: markers[i]}}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("creating marker ConfigMap in %s: %v", namespaces[i], err)
		}
	}
	t.Cleanup(func() {
		//nolint:usetesting // cleanup must survive testing.T's context cancellation
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		for _, namespace := range namespaces {
			if err := client.CoreV1().Namespaces().Delete(cleanupCtx, namespace, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("cleanup namespace %s: %v", namespace, err)
			}
		}
	})

	a := Launch(t, WithScopeNamespace(namespaces[0]))
	a.WaitLoaded(Connect)
	a.gotoKind(t, "configmaps", "ConfigMaps")
	a.WaitFor(markers[0], Settle)
	baselineRuntime := settledSnapshot(a)
	baselineRequests := a.Proxy().Counts()

	for i := 1; i < count; i++ {
		switchNamespaceThroughPalette(t, a, namespaces[i])
		a.WaitForAll(Settle, namespaces[i], markers[i])
		if latency := a.InputFence(); latency > soakInputBudget {
			t.Fatalf("input fence after namespace %d took %s, budget %s", i, latency, soakInputBudget)
		}
	}

	settled := settledSnapshot(a)
	requests := a.Proxy().Counts()
	watches := requests.ActiveByResourceVerb["configmaps/WATCH"]
	if watches != count {
		t.Errorf("active ConfigMap watches = %d, want one for each of %d visited namespaces", watches, count)
	}
	// Kubernetes 1.35/client-go may fill a new informer with a streaming
	// list (sendInitialEvents on the WATCH) rather than a separate LIST. Total
	// WATCHs still proves one informer per namespace on both request shapes.
	newWatches := requests.ByResourceVerb["configmaps/WATCH"] - baselineRequests.ByResourceVerb["configmaps/WATCH"]
	if newWatches < count-1 || newWatches > count {
		t.Errorf("new ConfigMap WATCHs = %d, want one per newly visited namespace (%d)", newWatches, count-1)
	}
	assertRuntimeBudget(t, baselineRuntime, settled, uint64(count-1)*uint64(8<<20)+uint64(32<<20), (count-1)*16+40)
	requireOnlyWatchesActive(t, requests)

	// Retention means revisiting the first namespace is a cache read, not a
	// new factory or informer. Request totals, not just active counts, catch a
	// stop-and-recreate implementation that happens to end at the same size.
	beforeReturn := a.Proxy().Counts()
	switchNamespaceThroughPalette(t, a, namespaces[0])
	a.WaitFor(markers[0], Settle)
	afterReturn := a.Proxy().Counts()
	for _, key := range []string{"configmaps/LIST", "configmaps/WATCH"} {
		if delta := afterReturn.ByResourceVerb[key] - beforeReturn.ByResourceVerb[key]; delta != 0 {
			t.Errorf("revisiting retained namespace started %d new %s requests", delta, key)
		}
	}
}
