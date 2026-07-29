//go:build e2e

package kube

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// The laziness rules are the ones a rendering test cannot catch: a regression
// here looks identical on screen and pulls the cluster down the wire. The
// unit-level versions of these (lazy_test.go, helm_secrets_test.go) assert
// against the fake clientset's recorded actions; these assert the same rules
// against a real apiserver, where the informer machinery, the discovery API
// and RBAC are all the real thing.
//
// They live in package kube rather than test/e2e because they read the
// unexported kindInformers/helmInformers maps directly — the same reason
// count_test.go does.
//
// Run: scripts/e2e-cluster.sh up && go test -tags e2e ./internal/kube/...

const e2eNamespace = "kute-e2e"

// e2eCluster builds a Cluster against the kind cluster scripts/e2e-cluster.sh
// provisions, started and synced, and stops it at the end of the test.
func e2eCluster(t *testing.T) *Cluster {
	t.Helper()
	return e2eClusterWithKubeconfig(t, e2eKubeconfig(t))
}

func e2eClusterWithKubeconfig(t *testing.T, path string) *Cluster {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no e2e cluster: %v\nrun: scripts/e2e-cluster.sh up", err)
	}

	// SetKubeconfigPath is process-global, so nothing here may run in
	// parallel — the same constraint the color-profile goldens live under.
	SetKubeconfigPath(path)
	t.Cleanup(func() { SetKubeconfigPath("") })

	c, err := NewCluster()
	if err != nil {
		t.Fatalf("NewCluster against %s: %v", path, err)
	}
	t.Cleanup(c.Stop)

	// Start in a goroutine and gate on KindSynced, exactly as app.run does —
	// never on Start returning. Start blocks until every *eager* cache has
	// synced, and under an identity that cannot list Namespaces or Nodes two
	// of those three never will. That is not a failure to connect: it is the
	// case CLAUDE.md is describing when it says Synced() is a connect latch
	// rather than cache state, and KindSynced is the per-kind answer.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Start(ctx) }()

	// Pod is eager and readable by every identity these tests use, so it is
	// the signal that the factory is running and the caches are filling.
	waitForKindSynced(t, c, KindPod, 90*time.Second)
	return c
}

func e2eKubeconfig(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("KUTE_E2E_KUBECONFIG"); p != "" {
		return p
	}
	return filepath.Join(repoRootForTest(t), ".kube", "e2e.config")
}

// e2ePartialKubeconfig is the kute-partial identity: cluster-wide read on the
// kinds kute needs to connect, and nothing on Deployments or Secrets. The
// ultra-narrow kute-restricted identity is deliberately *not* used here —
// under it even Pods are forbidden, because kute's informers list every kind
// cluster-wide regardless of the selected namespace, so there is no readable
// kind left to contrast a forbidden one against.
func e2ePartialKubeconfig(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("KUTE_E2E_PARTIAL_KUBECONFIG"); p != "" {
		return p
	}
	return filepath.Join(repoRootForTest(t), ".kube", "e2e-partial.config")
}

// repoRootForTest resolves <root> from internal/kube, where these tests run.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// startedKinds snapshots which typed informers exist right now.
func startedKinds(c *Cluster) map[ResourceKind]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[ResourceKind]bool, len(c.kindInformers))
	for kind := range c.kindInformers {
		out[kind] = true
	}
	return out
}

// TestConnectStartsOnlyTheEagerKinds is the whole "the app pays for the
// screen you're on" rule at its starting point: connecting to a real cluster
// registers three informers and no others.
func TestConnectStartsOnlyTheEagerKinds(t *testing.T) {
	c := e2eCluster(t)

	// Named literally rather than compared against eagerKinds: a test that
	// reads the set it is checking passes just as happily when someone adds
	// every kind to it, which is the exact regression this exists to catch.
	// Growing the eager set is a deliberate decision, and updating this line
	// is how it gets made.
	want := map[ResourceKind]bool{KindNamespace: true, KindPod: true, KindNode: true}

	started := startedKinds(c)
	for kind := range want {
		if !started[kind] {
			t.Errorf("eager kind %s has no informer after Start", kind)
		}
	}
	for kind := range started {
		if !want[kind] {
			t.Errorf("Start registered an informer for %s, which is not eager — connect must not pay for it", kind)
		}
	}
	if len(started) != len(want) {
		t.Fatalf("Start registered %d informers (%v), want exactly %v", len(started), started, want)
	}
}

// TestCountLiveStartsNoInformers: the goto palette shows a count per kind,
// and reading those from informer caches would start a watch per kind the
// moment the palette opened — the regression that once hung the app for a
// minute on the update loop.
func TestCountLiveStartsNoInformers(t *testing.T) {
	c := e2eCluster(t)
	before := len(startedKinds(c))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A dozen kinds — deliberately including the heavy ones a palette open
	// would otherwise pull: Secrets, Events, ReplicaSets.
	for _, kind := range []ResourceKind{
		KindDeployment, KindStatefulSet, KindDaemonSet, KindReplicaSet,
		KindService, KindConfigMap, KindSecret, KindIngress,
		KindJob, KindCronJob, KindEvent, KindPersistentVolumeClaim,
	} {
		if _, err := c.CountLive(ctx, kind, e2eNamespace); err != nil {
			t.Fatalf("CountLive(%s): %v", kind, err)
		}
	}

	if after := len(startedKinds(c)); after != before {
		t.Fatalf("CountLive over 12 kinds took the informer count from %d to %d; it must not touch them at all",
			before, after)
	}
}

// TestCountLiveCountsRealObjects pins the other half of CountLive: it counts
// server-side, off remainingItemCount, so the number has to be right as well
// as cheap.
func TestCountLiveCountsRealObjects(t *testing.T) {
	c := e2eCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The fixtures are exactly two Deployments in kute-e2e.
	got, err := c.CountLive(ctx, KindDeployment, e2eNamespace)
	if err != nil {
		t.Fatalf("CountLive(Deployment, %s): %v", e2eNamespace, err)
	}
	if got != 2 {
		t.Fatalf("CountLive(Deployment, %s) = %d, want 2 (fixtures api and worker)", e2eNamespace, got)
	}
}

// TestFirstReadStartsExactlyOneInformer: laziness lives in ensureKind, on the
// read path. One read of one kind starts that kind's informer and nothing
// else's.
func TestFirstReadStartsExactlyOneInformer(t *testing.T) {
	c := e2eCluster(t)
	before := startedKinds(c)
	if before[KindSecret] {
		t.Fatal("Secret already has an informer before anything read it")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := c.ListRaw(ctx, KindSecret, e2eNamespace); err != nil {
		t.Fatalf("ListRaw(Secret): %v", err)
	}

	after := startedKinds(c)
	if !after[KindSecret] {
		t.Fatal("reading Secrets started no Secret informer")
	}
	if len(after) != len(before)+1 {
		t.Fatalf("one read took the informer count from %d to %d, want %d: %v",
			len(before), len(after), len(before)+1, after)
	}

	// And the cache really fills — a lazily-started informer that nobody
	// waits on correctly is the other half of this bug.
	waitForKindSynced(t, c, KindSecret, 60*time.Second)
	objs, err := c.ListRaw(ctx, KindSecret, e2eNamespace)
	if err != nil {
		t.Fatalf("second ListRaw(Secret): %v", err)
	}
	if len(objs) == 0 {
		t.Fatal("the Secret cache synced empty; kute-e2e has fixture Secrets in it")
	}
}

// TestHelmInformerIsPerNamespaceAndTypeFiltered pins docs/lazy-informers.md
// §5.5: release Secrets are individually large, so their informer is one per
// namespace read and filtered server-side to type=helm.sh/release.v1 rather
// than sharing the general Secret cache. Over a VPN that difference (8.19 MB
// cluster-wide against 4 MB for one namespace) is a screen that loads against
// one that never does.
func TestHelmInformerIsPerNamespaceAndTypeFiltered(t *testing.T) {
	c := e2eCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The first read starts the informer and returns an empty cache; the
	// second, after it has synced, is the one with releases in it.
	if _, err := c.ListHelmReleaseSecrets(ctx, e2eNamespace); err != nil {
		t.Fatalf("ListHelmReleaseSecrets: %v", err)
	}
	waitForKindSynced(t, c, KindHelmRelease, 60*time.Second)
	objs, err := c.ListHelmReleaseSecrets(ctx, e2eNamespace)
	if err != nil {
		t.Fatalf("ListHelmReleaseSecrets after sync: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("got %d release Secrets, want the fixture's 2 revisions", len(objs))
	}

	// Per-namespace: keyed by the namespace read, not by "" (cluster-wide).
	c.mu.Lock()
	_, namespaced := c.helmInformers[e2eNamespace]
	_, clusterWide := c.helmInformers[""]
	c.mu.Unlock()
	if !namespaced {
		t.Errorf("no helm informer for %s; the read was namespace-scoped", e2eNamespace)
	}
	if clusterWide {
		t.Errorf("a cluster-wide helm Secret informer was started for a namespaced read")
	}

	// Type-filtered server-side: the cache holds only release Secrets. If
	// the field selector were dropped the LIST would return every Secret in
	// the namespace, and app-secret would be sitting in this store.
	c.mu.Lock()
	informer := c.helmInformers[e2eNamespace]
	c.mu.Unlock()
	for _, obj := range informer.GetStore().List() {
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			t.Fatalf("helm cache holds a %T", obj)
		}
		if secret.Type != HelmReleaseSecretType {
			t.Fatalf("the helm cache holds %s/%s of type %q — the type=%s field selector is not reaching the server",
				secret.Namespace, secret.Name, secret.Type, HelmReleaseSecretType)
		}
	}

	// And reading releases did not populate — or need — the shared Secret
	// cache, which is the cost this informer exists to avoid.
	if startedKinds(c)[KindSecret] {
		t.Error("listing Helm releases started the shared Secret informer")
	}
}

// TestForbiddenKindIsSettledNotPending is the anti-hang half of the rule a
// forbidden cache has to satisfy: under an identity that cannot read a kind,
// KindSynced reports settled, so a loading state gated on it comes off its
// spinner instead of waiting for a cache that is never going to fill.
//
// It does *not* assert KindError. Permission failures deliberately travel
// their own path — markKindFailed, plus the Forbidden error a screen's own
// read returns — rather than the kindStalled map KindError reads, which is
// for an initial LIST that is still failing but might yet succeed. Asserting
// a non-nil KindError here would be testing against the code's stated design,
// so this pins the two things that actually distinguish "forbidden" from
// "the cluster has none of these": settled, and marked failed.
func TestForbiddenKindIsSettledNotPending(t *testing.T) {
	c := e2eClusterWithKubeconfig(t, e2ePartialKubeconfig(t))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// kute-partial has no read on Deployments anywhere, so the reflector's
	// LIST is Forbidden at the apiserver. The read itself returns an empty
	// cache rather than an error — reads go through the informer, which has
	// nothing in it.
	if _, err := c.ListRaw(ctx, KindDeployment, e2eNamespace); err != nil {
		t.Fatalf("ListRaw(Deployment) under the restricted SA: %v", err)
	}

	// Settled, without the cache ever filling.
	waitForKindSynced(t, c, KindDeployment, 60*time.Second)

	// And settled *because it is known not to be coming*, not because the
	// informer reported a successful empty LIST. Without this the assertion
	// above would pass just as well for a kind that genuinely has none.
	waitForKindFailed(t, c, KindDeployment, 60*time.Second)

	// KindError stays nil for a permission failure, by design. Pinned so the
	// two paths can't be quietly merged without this test having an opinion.
	if err := c.KindError(KindDeployment); err != nil {
		t.Fatalf("KindError(Deployment) = %v, want nil: permission failures travel markKindFailed, not kindStalled", err)
	}
}

// TestReadableKindSurvivesAForbiddenNeighbour: one kind being forbidden is a
// permission boundary, not a broken cluster. The kind this identity *can*
// read has to keep reading.
func TestReadableKindSurvivesAForbiddenNeighbour(t *testing.T) {
	c := e2eClusterWithKubeconfig(t, e2ePartialKubeconfig(t))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Provoke the forbidden read first, so this is genuinely "after a 403".
	if _, err := c.ListRaw(ctx, KindDeployment, e2eNamespace); err != nil {
		t.Fatalf("ListRaw(Deployment): %v", err)
	}
	waitForKindFailed(t, c, KindDeployment, 60*time.Second)

	waitForKindSynced(t, c, KindPod, 60*time.Second)
	if err := c.KindError(KindPod); err != nil {
		t.Fatalf("KindError(Pod) = %v, want nil — the partial SA can list pods", err)
	}
	pods, err := c.ListRaw(ctx, KindPod, e2eNamespace)
	if err != nil {
		t.Fatalf("ListRaw(Pod): %v", err)
	}
	if len(pods) == 0 {
		t.Fatal("the partial SA read no pods; kute-e2e is running fixture workloads")
	}
}

func waitForKindSynced(t *testing.T, c *Cluster, kind ResourceKind, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if c.KindSynced(kind) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("KindSynced(%s) never became true within %s — a loading state gated on it would hang forever", kind, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitForKindFailed waits for kind to be marked permanently failed — the
// permission path's own signal, set from the reflector's goroutine when its
// watch reports a 403.
func waitForKindFailed(t *testing.T, c *Cluster, kind ResourceKind, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		c.mu.Lock()
		failed := c.kindFailed[kind]
		c.mu.Unlock()
		if failed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never marked failed within %s — a forbidden cache that reads as merely empty is a lie about the cluster", kind, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
