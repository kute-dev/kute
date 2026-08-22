package kube

import (
	"context"
	"fmt"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
)

// Tests for docs/lazy-informers.md §5.6's opt-in namespace
// scoping. newLazyTestCluster's Cluster starts unscoped (the zero value of
// the new `scoped` field) — SetNamespaceScope is what turns scoping on, the
// same call BuildSession makes for a real launch before Start.

// listActionNamespaces returns the namespace each recorded LIST of resource
// was issued against ("" = cluster-wide) — the scope-layer counterpart of
// helm_secrets_test.go's secretListNamespaces, generalized to any resource.
func listActionNamespaces(cs interface{ Actions() []k8stesting.Action }, resource string) []string {
	var out []string
	for _, a := range cs.Actions() {
		if a.GetVerb() != "list" || a.GetResource().Resource != resource {
			continue
		}
		out = append(out, a.GetNamespace())
	}
	return out
}

// TestClusterWideModeNormalizesEveryScopeToEmpty pins the byte-for-byte
// compatibility decision: with SetNamespaceScope never called, cacheScope
// answers "" regardless of what namespace a read names — the single fact
// every other unscoped test in this package already exercises indirectly.
func TestClusterWideModeNormalizesEveryScopeToEmpty(t *testing.T) {
	t.Parallel()
	c, _ := newLazyTestCluster()
	defer c.Stop()
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, ns := range []string{"", "default", "team-a", "team-b"} {
		if got := c.cacheScope(KindConfigMap, ns); got != "" {
			t.Errorf("cacheScope(ConfigMap, %q) = %q in cluster-wide mode, want \"\"", ns, got)
		}
	}
}

// TestScopedModeEagerPodListCarriesTheNamespace is §3's eager-set rule:
// Namespace and Node stay cluster-wide (there is only ever one cache for
// either), but Pod's eager cache scopes to the launch namespace.
func TestScopedModeEagerPodListCarriesTheNamespace(t *testing.T) {
	t.Parallel()
	c, cs := newLazyTestCluster()
	defer c.Stop()
	c.SetNamespaceScope("team-a")
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the eager pod list", func() bool { return countListActions(cs, "pods") > 0 })

	for _, ns := range listActionNamespaces(cs, "pods") {
		if ns != "team-a" {
			t.Fatalf("eager Pod list ran against namespace %q, want \"team-a\"", ns)
		}
	}
	for _, resource := range []string{"namespaces", "nodes"} {
		waitFor(t, resource+" to be listed", func() bool { return countListActions(cs, resource) > 0 })
		for _, ns := range listActionNamespaces(cs, resource) {
			if ns != "" {
				t.Fatalf("%s stayed cluster-wide in scoped mode, got namespace %q, want \"\"", resource, ns)
			}
		}
	}
}

// TestScopedModeFirstReadStartsExactlyOneScopedInformer mirrors
// TestRepeatedReadsStartTheInformerOnce for scoped mode: repeated reads of
// the same (kind, namespace) reuse the one informer ensureKind already
// started, scoped or not.
func TestScopedModeFirstReadStartsExactlyOneScopedInformer(t *testing.T) {
	t.Parallel()
	c, cs := newLazyTestCluster()
	defer c.Stop()
	c.SetNamespaceScope("team-a")
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx := t.Context()
	for range 20 {
		if _, err := c.ListRaw(ctx, KindConfigMap, "team-a"); err != nil {
			t.Fatalf("ListRaw(ConfigMap): %v", err)
		}
	}
	waitFor(t, "the scoped ConfigMap informer to list", func() bool {
		return countListActions(cs, "configmaps") > 0
	})
	time.Sleep(50 * time.Millisecond)
	if n := countListActions(cs, "configmaps"); n != 1 {
		t.Fatalf("configmaps listed %d times across 20 reads, want exactly 1", n)
	}
	for _, ns := range listActionNamespaces(cs, "configmaps") {
		if ns != "team-a" {
			t.Fatalf("scoped ConfigMap list ran against namespace %q, want \"team-a\"", ns)
		}
	}
}

// TestScopedModeNamespacesAreIndependentCaches is §1's core claim: two named
// namespaces and the explicit "" global cache are three separate informers,
// each started by its own first read.
func TestScopedModeNamespacesAreIndependentCaches(t *testing.T) {
	t.Parallel()
	c, cs := newLazyTestCluster()
	defer c.Stop()
	c.SetNamespaceScope("team-a")
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx := t.Context()
	for _, ns := range []string{"team-a", "team-b", ""} {
		if _, err := c.ListRaw(ctx, KindConfigMap, ns); err != nil {
			t.Fatalf("ListRaw(ConfigMap, %q): %v", ns, err)
		}
	}
	waitFor(t, "all three ConfigMap caches to list", func() bool {
		return countListActions(cs, "configmaps") >= 3
	})

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ns := range []string{"team-a", "team-b", ""} {
		if _, ok := c.kindInformers[scopeKey{KindConfigMap, ns}]; !ok {
			t.Errorf("no informer registered for ConfigMap@%q", ns)
		}
	}
	if len(c.scopedFactories) != 2 {
		t.Errorf("scopedFactories has %d entries, want 2 (team-a, team-b) — \"\" uses the shared cluster-wide factory", len(c.scopedFactories))
	}
}

// TestScopedModeClusterScopedKindIgnoresNamespace: Node has no per-namespace
// lister at all, so even a scoped session must not fragment it into one
// informer per namespace touched.
func TestScopedModeClusterScopedKindIgnoresNamespace(t *testing.T) {
	t.Parallel()
	c, cs := newLazyTestCluster()
	defer c.Stop()
	c.SetNamespaceScope("team-a")
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Node is already eager at "" from Start; reading it again for a named
	// namespace must not start a second, wrongly-scoped informer.
	if _, err := c.ListRaw(t.Context(), KindNode, "team-a"); err != nil {
		t.Fatalf("ListRaw(Node): %v", err)
	}
	waitFor(t, "the node list", func() bool { return countListActions(cs, "nodes") > 0 })
	time.Sleep(50 * time.Millisecond)

	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for key := range c.kindInformers {
		if key.kind == KindNode {
			n++
			if key.namespace != "" {
				t.Errorf("Node informer registered at scope %q, want \"\"", key.namespace)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d Node informers registered, want exactly 1", n)
	}
}

// TestScopedModeDenialInOneNamespaceDoesNotPoisonAnother is §1's other core
// claim, stated as a behavior rather than a map-shape assertion: a Forbidden
// LIST against one namespace must not make KindForbidden lie about a
// namespace that was never refused.
func TestScopedModeDenialInOneNamespaceDoesNotPoisonAnother(t *testing.T) {
	t.Parallel()
	c, cs := newLazyTestCluster()
	defer c.Stop()
	cs.PrependReactor("list", "configmaps", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == "team-a" {
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Resource: "configmaps"}, "", fmt.Errorf("nope"))
		}
		return false, nil, nil
	})
	c.SetNamespaceScope("team-a")
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := c.ListRaw(t.Context(), KindConfigMap, "team-a"); err != nil {
		t.Fatalf("ListRaw(ConfigMap, team-a): %v", err)
	}
	waitFor(t, "team-a to be marked forbidden", func() bool {
		return c.KindForbidden(KindConfigMap, "team-a") != nil
	})

	if _, err := c.ListRaw(t.Context(), KindConfigMap, "team-b"); err != nil {
		t.Fatalf("ListRaw(ConfigMap, team-b): %v", err)
	}
	waitFor(t, "team-b's cache to sync", func() bool { return c.KindSynced(KindConfigMap, "team-b") })
	if err := c.KindForbidden(KindConfigMap, "team-b"); err != nil {
		t.Fatalf("KindForbidden(ConfigMap, team-b) = %v, want nil — only team-a was refused", err)
	}
}

// TestStartToleratesEagerClusterScopedKindsForbidden is §3's permission-
// tolerant startup: a denied Namespace or Node cache is a missing
// capability, not a failed connection, so Start must still return
// successfully and latch Synced rather than hanging or erroring — the shape
// a namespace-bound identity actually hits (its Role grants nothing
// cluster-scoped at all).
func TestStartToleratesEagerClusterScopedKindsForbidden(t *testing.T) {
	t.Parallel()
	c, cs := newLazyTestCluster()
	defer c.Stop()
	forbidden := func(a k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: a.GetResource().Resource}, "", fmt.Errorf("nope"))
	}
	cs.PrependReactor("list", "namespaces", forbidden)
	cs.PrependReactor("list", "nodes", forbidden)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start returned %v; a denied eager cache must not block startup", err)
	}
	if !c.Synced() {
		t.Fatal("Synced() = false after Start returned; a denied eager cache must still latch the connect")
	}
	if got := c.KindForbidden(KindNamespace, ""); !IsPermissionError(got) {
		t.Fatalf("KindForbidden(Namespace) = %v, want the denial", got)
	}
	if got := c.KindForbidden(KindNode, ""); !IsPermissionError(got) {
		t.Fatalf("KindForbidden(Node) = %v, want the denial", got)
	}
	// Pod was never forbidden, so it should have synced normally and Start
	// should not have waited on Namespace/Node beyond their own denial.
	if !c.KindSynced(KindPod, "") {
		t.Fatal("KindSynced(Pod) = false; the eager Pod cache should have synced independently of the denied cluster-scoped ones")
	}
}

// TestGenerationGuardDropsStaleCallbacks is §3's late-callback protection in
// isolation: a captured generation from before a context replacement must
// read as stale afterward, which is the single check every watch-error/
// notify closure relies on to avoid writing into the new context's state.
func TestGenerationGuardDropsStaleCallbacks(t *testing.T) {
	t.Parallel()
	c, _ := newLazyTestCluster()
	defer c.Stop()
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	c.mu.Lock()
	gen := c.generation
	c.mu.Unlock()
	if !c.generationCurrent(gen) {
		t.Fatal("a freshly captured generation must read as current")
	}

	// Simulate what SwitchContext's own Start does: bump the generation.
	c.mu.Lock()
	c.generation++
	c.mu.Unlock()
	if c.generationCurrent(gen) {
		t.Fatal("a bumped generation must invalidate the old snapshot — a late callback would otherwise corrupt the new context's state")
	}
}

// TestScopedModeConcurrentFirstReadsAreRaceFree exercises the same
// contention TestLazyReadIsRaceFree does, but across scoped reads of
// several namespaces at once — the concurrent map access
// scopedFactories/dynScopedFactories add on top of the unscoped path.
// Meaningful under `go test -race`; still a useful deadlock/panic smoke
// test without it.
func TestScopedModeConcurrentFirstReadsAreRaceFree(t *testing.T) {
	t.Parallel()
	c, _ := newLazyTestCluster()
	defer c.Stop()
	c.SetNamespaceScope("team-a")
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx := t.Context()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for _, ns := range []string{"team-a", "team-b", "team-c", ""} {
				_, _ = c.ListRaw(ctx, KindSecret, ns)
				_, _ = c.ListRaw(ctx, KindConfigMap, ns)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
