package kube

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

// newLazyTestCluster builds a Cluster over fake clients whose recorded
// actions are the assertion surface: which resources got listed, and when.
func newLazyTestCluster(objs ...runtime.Object) (*Cluster, *fake.Clientset) {
	cs := fake.NewSimpleClientset(objs...)
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	// The CRD informer Start opens is a dynamic one, and the dynamic fake
	// needs each GVR's list kind spelled out up front.
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		crdGVR: "CustomResourceDefinitionList",
	})
	return &Cluster{
		clientset:  cs,
		factory:    newTypedFactory(cs),
		dynClient:  dyn,
		dynFactory: dynamicinformer.NewDynamicSharedInformerFactory(dyn, 0),
		stopCh:     make(chan struct{}),
		events:     make(chan ResourceChangedMsg, 256),
		health:     newHealth(),
	}, cs
}

// listedResources reports which resource types the clientset was asked to
// list, deduplicated and sorted.
func listedResources(cs *fake.Clientset) []string {
	seen := map[string]bool{}
	for _, a := range cs.Actions() {
		if a.GetVerb() == "list" {
			seen[a.GetResource().Resource] = true
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestStartOnlyListsEagerKinds is the regression test for the whole feature:
// connecting must not pull every kind in the cluster down the wire. Before
// this, all 21 typed informers listed before Start returned.
func TestStartOnlyListsEagerKinds(t *testing.T) {
	c, cs := newLazyTestCluster(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"}},
	)
	defer c.Stop()

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := listedResources(cs)
	want := []string{"namespaces", "nodes", "pods"}
	if len(got) != len(want) {
		t.Fatalf("connect listed %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("connect listed %v, want %v", got, want)
		}
	}
}

// TestListRawStartsTheKindOnFirstRead: reading a lazy kind is what starts
// its informer, so no caller has to declare anything.
func TestListRawStartsTheKindOnFirstRead(t *testing.T) {
	c, cs := newLazyTestCluster()
	defer c.Stop()
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	for _, r := range listedResources(cs) {
		if r == "secrets" {
			t.Fatal("secrets were listed at connect; Secret is supposed to be lazy")
		}
	}

	if _, err := c.ListRaw(context.Background(), KindSecret, "default"); err != nil {
		t.Fatalf("ListRaw(Secret): %v", err)
	}

	waitFor(t, "the Secret informer to list", func() bool {
		for _, r := range listedResources(cs) {
			if r == "secrets" {
				return true
			}
		}
		return false
	})
}

// TestRepeatedReadsStartTheInformerOnce guards ensureKind's idempotence:
// browse re-reads on every change event and every 250ms retry, so a
// non-idempotent start would relist the world continuously.
func TestRepeatedReadsStartTheInformerOnce(t *testing.T) {
	c, cs := newLazyTestCluster()
	defer c.Stop()
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx := context.Background()
	for i := 0; i < 20; i++ {
		if _, err := c.ListRaw(ctx, KindConfigMap, ""); err != nil {
			t.Fatalf("ListRaw(ConfigMap): %v", err)
		}
	}
	waitFor(t, "the ConfigMap informer to list", func() bool {
		return countListActions(cs, "configmaps") > 0
	})
	// Let any duplicate starts settle before counting.
	time.Sleep(50 * time.Millisecond)

	if n := countListActions(cs, "configmaps"); n != 1 {
		t.Fatalf("configmaps listed %d times across 20 reads, want exactly 1", n)
	}
}

func countListActions(cs *fake.Clientset, resource string) int {
	n := 0
	for _, a := range cs.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == resource {
			n++
		}
	}
	return n
}

// TestLazyReadIsRaceFree: ListRaw is called from the Bubble Tea update loop
// while informers and the health loop run on their own goroutines, so the
// start path has to be safe under concurrent first-reads of the same kind.
func TestLazyReadIsRaceFree(t *testing.T) {
	c, _ := newLazyTestCluster()
	defer c.Stop()
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, kind := range []ResourceKind{KindSecret, KindConfigMap, KindEvent, KindJob} {
				_, _ = c.ListRaw(ctx, kind, "default")
			}
		}()
	}
	wg.Wait()
}

// TestListRawAfterStopDoesNotStartInformers: Stop nils stopCh, and an
// informer started with a nil channel would never stop — a goroutine leak
// on the way out.
func TestListRawAfterStopDoesNotStartInformers(t *testing.T) {
	c, cs := newLazyTestCluster()
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	c.Stop()

	before := len(cs.Actions())
	if _, err := c.ListRaw(context.Background(), KindSecret, "default"); err != nil {
		t.Fatalf("ListRaw after Stop should read an empty cache, not error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if got := len(cs.Actions()); got != before {
		t.Fatalf("ListRaw after Stop issued %d new API actions, want 0", got-before)
	}
	c.mu.Lock()
	_, registered := c.kindInformers[KindSecret]
	c.mu.Unlock()
	if registered {
		t.Fatal("ListRaw after Stop registered an informer that can never be stopped")
	}
}

// TestLazyStartReArmsTheConnectGrace pins the health companion fix. A lazy
// start produces the same LIST contention a cold connect does, so a /livez
// timeout during it must stay forgiven — otherwise opening Secrets on a slow
// link paints an OFFLINE banner over a healthy cluster.
func TestLazyStartReArmsTheConnectGrace(t *testing.T) {
	c, _ := newLazyTestCluster()
	defer c.Stop()
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Age the window past the grace period, as a long session would.
	c.health.mu.Lock()
	c.health.startedAt = time.Now().Add(-2 * connectGrace)
	c.health.mu.Unlock()

	c.ensureKind(KindSecret)

	c.health.mu.Lock()
	age := time.Since(c.health.startedAt)
	c.health.mu.Unlock()
	if age > connectGrace {
		t.Fatalf("grace window still %v old after a lazy start; a mid-session LIST burst is unforgiven", age)
	}
}

// TestStartIgnoresAStrayListerRegistration is the hazard the typedKinds
// table was built to close: a read registers an informer, and the next
// factory.Start would run it. Every registration now goes through
// registerWatches, so the informer that read started is fully wired.
func TestStartIgnoresAStrayListerRegistration(t *testing.T) {
	c, _ := newLazyTestCluster()
	defer c.Stop()
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := c.ListRaw(context.Background(), KindReplicaSet, ""); err != nil {
		t.Fatalf("ListRaw(ReplicaSet): %v", err)
	}
	c.mu.Lock()
	inf, ok := c.kindInformers[KindReplicaSet]
	c.mu.Unlock()
	if !ok || inf == nil {
		t.Fatal("reading a kind must register its informer, not leave it dangling on the factory")
	}
	// A registered informer is a watched one: its handler feeds notify.
	waitFor(t, "the ReplicaSet informer to sync", func() bool { return c.KindSynced(KindReplicaSet) })
}
