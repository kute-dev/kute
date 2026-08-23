//go:build e2e

package kube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// The wire-level counterpart of test/e2e/scoped_test.go: these read
// c.kindInformers directly, which only package kube can do — the same reason
// e2e_lazy_test.go's tests live here rather than in test/e2e. See
// docs/lazy-informers.md §5.6.
//
// Run: scripts/e2e-cluster.sh up && go test -tags e2e ./internal/kube/... -run TestScoped

// e2eRestrictedKubeconfig is the kute-restricted identity's kubeconfig —
// pods list/get/watch in kute-e2e and nothing else, anywhere. package kube
// cannot import test/e2e to reuse RestrictedKubeconfigPath() (that import
// would run backwards: test/e2e imports internal/app, which imports
// internal/kube), so this re-derives the same path from the same env var,
// mirroring e2ePartialKubeconfig above.
func e2eRestrictedKubeconfig(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("KUTE_E2E_RESTRICTED_KUBECONFIG"); p != "" {
		return p
	}
	return filepath.Join(repoRootForTest(t), ".kube", "e2e-restricted.config")
}

func TestScopedNamespaceCachesStayDistinctCurrentAndExplicit(t *testing.T) {
	c := e2eScopedCluster(t, e2eTeamKubeconfig(t), e2eNamespace)
	const secondNamespace = "kute-e2e-b"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if _, err := c.ListRaw(ctx, KindConfigMap, e2eNamespace); err != nil {
		t.Fatalf("ListRaw ConfigMap A: %v", err)
	}
	waitForKindSynced(t, c, KindConfigMap, e2eNamespace, 60*time.Second)
	c.SwitchNamespace(secondNamespace)
	if _, err := c.ListRaw(ctx, KindConfigMap, secondNamespace); err != nil {
		t.Fatalf("ListRaw ConfigMap B: %v", err)
	}
	waitForKindSynced(t, c, KindConfigMap, secondNamespace, 60*time.Second)

	scopes := startedScopes(c)
	for _, key := range []scopeKey{{KindConfigMap, e2eNamespace}, {KindConfigMap, secondNamespace}} {
		if !scopes[key] {
			t.Errorf("missing scoped cache key %+v: %v", key, scopes)
		}
	}
	if got := countKindScopes(scopes, KindConfigMap); got != 2 {
		t.Fatalf("ConfigMap informer scopes = %d, want A and B only: %v", got, scopes)
	}

	// Both scoped informers stay live. Mutating A while B is the active UI
	// namespace must already be reflected when A is revisited.
	adminCfg, err := clientcmd.BuildConfigFromFlags("", e2eKubeconfig(t))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := kubernetes.NewForConfig(adminCfg)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("scoped-current-%d", time.Now().UnixNano())
	_, err = admin.CoreV1().ConfigMaps(e2eNamespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name}, Data: map[string]string{"marker": "current"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = admin.CoreV1().ConfigMaps(e2eNamespace).Delete(cleanupCtx, name, metav1.DeleteOptions{})
	})
	waitForCachedName(t, c, KindConfigMap, e2eNamespace, name, 30*time.Second)

	c.SwitchNamespace(e2eNamespace)
	if _, err := c.ListRaw(ctx, KindConfigMap, e2eNamespace); err != nil {
		t.Fatal(err)
	}
	if got := countKindScopes(startedScopes(c), KindConfigMap); got != 2 {
		t.Fatalf("revisiting A created another informer: %d", got)
	}

	// An explicit all-namespaces read owns the explicit global key. The team
	// Role cannot satisfy it, but it must never be silently downgraded to A.
	_, _ = c.ListRaw(ctx, KindConfigMap, "")
	_ = waitForKindForbidden(t, c, KindConfigMap, "", 60*time.Second)
	if !startedScopes(c)[scopeKey{KindConfigMap, ""}] {
		t.Fatalf("explicit global read did not start the global cache: %v", startedScopes(c))
	}
}

func countKindScopes(scopes map[scopeKey]bool, kind ResourceKind) int {
	count := 0
	for key := range scopes {
		if key.kind == kind {
			count++
		}
	}
	return count
}

func waitForCachedName(t *testing.T, c *Cluster, kind ResourceKind, namespace, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		objs, err := c.ListRaw(context.Background(), kind, namespace)
		if err == nil {
			for _, obj := range objs {
				if accessor, err := apimeta.Accessor(obj); err == nil && accessor.GetName() == name {
					return
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("cache %s/%s never received %s", kind, namespace, name)
}

// e2eTeamKubeconfig is the kute-team identity's kubeconfig — a namespace-
// bound Role in kute-e2e (Pods, ConfigMaps, Events, pods/log), no
// ClusterRole anywhere. The identity the scoped-mode suite launches under.
func e2eTeamKubeconfig(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("KUTE_E2E_TEAM_KUBECONFIG"); p != "" {
		return p
	}
	return filepath.Join(repoRootForTest(t), ".kube", "e2e-team.config")
}

// e2eScopedCluster builds a Cluster pinned to namespace via SetNamespaceScope,
// started and synced under kubeconfig — the scoped counterpart of
// e2eClusterWithKubeconfig, which never calls SetNamespaceScope and so always
// exercises cluster-wide mode.
//
// Start's error is captured through a buffered channel rather than launched
// with a bare `go func() { _ = c.Start(ctx) }()` and forgotten: the test
// below asserts Synced() has latched despite Namespace/Node being denied
// (the permission-tolerant-startup path), and that claim is only proven once
// Start has actually returned — waiting on Pod's own KindSynced alone can
// pass while Start is still blocked (or has failed outright) if Pod happens
// to sync before the eager set as a whole settles.
func e2eScopedCluster(t *testing.T, kubeconfig, namespace string) *Cluster {
	t.Helper()
	if _, err := os.Stat(kubeconfig); err != nil {
		// Same distinction harness.go's Launch makes: "nobody ran `up`" (the
		// admin config is missing too — Skip, nothing to do until they do)
		// versus "`up` ran, the admin config is right there, but this
		// identity's derived kubeconfig was never minted" (Fatal — that's
		// e2e-cluster.sh itself broken, and a Skip would misreport it as no
		// cluster at all).
		if _, adminErr := os.Stat(e2eKubeconfig(t)); adminErr == nil {
			t.Fatalf("admin e2e kubeconfig exists (%s) but %s does not — scripts/e2e-cluster.sh up did not mint it: %v", e2eKubeconfig(t), kubeconfig, err)
		}
		t.Skipf("no e2e cluster: %v\nrun: scripts/e2e-cluster.sh up", err)
	}
	SetKubeconfigPath(kubeconfig)
	t.Cleanup(func() { SetKubeconfigPath("") })

	c, err := NewCluster()
	if err != nil {
		t.Fatalf("NewCluster against %s: %v", kubeconfig, err)
	}
	c.SetNamespaceScope(namespace)
	t.Cleanup(c.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	startErr := make(chan error, 1)
	go func() { startErr <- c.Start(ctx) }()
	select {
	case err := <-startErr:
		if err != nil {
			t.Fatalf("Start against %s: %v", kubeconfig, err)
		}
	case <-time.After(90 * time.Second):
		t.Fatalf("Start against %s did not return within 90s", kubeconfig)
	}

	waitForKindSynced(t, c, KindPod, namespace, 90*time.Second)
	return c
}

// startedScopes snapshots which typed informers exist right now, keyed on
// the whole scopeKey{kind, namespace} rather than startedKinds' bare
// ResourceKind — a scoped read that accidentally landed cluster-wide would
// pass a startedKinds assertion just as happily as a correctly-scoped one,
// which is exactly the regression this suite exists to catch.
func startedScopes(c *Cluster) map[scopeKey]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[scopeKey]bool, len(c.kindInformers))
	for key := range c.kindInformers {
		out[key] = true
	}
	return out
}

// TestScopedModeEagerPodListCarriesTheNamespaceAgainstARealAPIServer is
// internal/kube/scoped_test.go's
// TestScopedModeEagerPodListCarriesTheNamespace (fake clientset) proven
// against kube-apiserver's own field-selector/RBAC enforcement instead.
func TestScopedModeEagerPodListCarriesTheNamespaceAgainstARealAPIServer(t *testing.T) {
	c := e2eScopedCluster(t, e2eRestrictedKubeconfig(t), e2eNamespace)

	if !startedScopes(c)[scopeKey{KindPod, e2eNamespace}] {
		t.Fatalf("Pod's eager informer did not land at scopeKey{Pod, %s}: %v", e2eNamespace, startedScopes(c))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pods, err := c.ListRaw(ctx, KindPod, e2eNamespace)
	if err != nil {
		t.Fatalf("ListRaw(Pod, %s): %v", e2eNamespace, err)
	}
	if len(pods) == 0 {
		t.Fatal("the scoped Pod cache synced empty; kute-e2e has fixture pods in it")
	}
}

// TestScopedModeClusterScopedReadsStayForbiddenUnderANamespacedRole is a
// real 403 from kube-apiserver for a Role with no ClusterRole, not a
// synthetic apierrors.NewForbidden from a PrependReactor the way
// internal/kube/scoped_test.go's fake-clientset version has to build it.
func TestScopedModeClusterScopedReadsStayForbiddenUnderANamespacedRole(t *testing.T) {
	c := e2eScopedCluster(t, e2eRestrictedKubeconfig(t), e2eNamespace)

	_ = waitForKindForbidden(t, c, KindNamespace, "", 60*time.Second)
	_ = waitForKindForbidden(t, c, KindNode, "", 60*time.Second)
}

// TestScopedModeStartLatchesSyncedDespiteTheDenials pins the
// permission-tolerant startup path (eagerHasSynced) as its own named
// regression, proven against real Forbidden responses arriving
// asynchronously off a real reflector goroutine rather than a synchronous
// PrependReactor return. e2eScopedCluster already blocks on Start returning
// (or timing out) before handing the cluster back, so by the time this
// test's body runs the interesting work already happened — asserting
// Synced() here is what turns that into a regression test of its own
// instead of a side effect of another test's setup.
func TestScopedModeStartLatchesSyncedDespiteTheDenials(t *testing.T) {
	c := e2eScopedCluster(t, e2eRestrictedKubeconfig(t), e2eNamespace)

	if !c.Synced() {
		t.Fatal("Synced() is false despite Start having returned — Namespace/Node being Forbidden must not stop the connect latch from settling")
	}
}

// TestScopedModeLazyReadStartsExactlyOneInformerAgainstTheRealServer is the
// scoped counterpart of TestFirstReadStartsExactlyOneInformer: a kind whose
// informer starts lazily, scoped to one namespace, under a Role that has
// never had cluster-wide list access at all.
func TestScopedModeLazyReadStartsExactlyOneInformerAgainstTheRealServer(t *testing.T) {
	c := e2eScopedCluster(t, e2eTeamKubeconfig(t), e2eNamespace)

	if startedScopes(c)[scopeKey{KindConfigMap, e2eNamespace}] {
		t.Fatal("ConfigMap already has a scoped informer before anything read it")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for i := 0; i < 20; i++ {
		if _, err := c.ListRaw(ctx, KindConfigMap, e2eNamespace); err != nil {
			t.Fatalf("ListRaw(ConfigMap, %s) read %d: %v", e2eNamespace, i, err)
		}
	}

	scopes := startedScopes(c)
	if !scopes[scopeKey{KindConfigMap, e2eNamespace}] {
		t.Fatal("reading ConfigMaps started no scoped ConfigMap informer")
	}
	count := 0
	for key := range scopes {
		if key.kind == KindConfigMap {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("20 reads of ConfigMap started %d informers, want exactly 1: %v", count, scopes)
	}

	waitForKindSynced(t, c, KindConfigMap, e2eNamespace, 60*time.Second)
	cms, err := c.ListRaw(ctx, KindConfigMap, e2eNamespace)
	if err != nil {
		t.Fatalf("ListRaw(ConfigMap, %s) after sync: %v", e2eNamespace, err)
	}
	if len(cms) == 0 {
		t.Fatal("the scoped ConfigMap cache synced empty; kute-e2e has fixture ConfigMaps in it")
	}
}
