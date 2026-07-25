package kube

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

func newSyncTestCluster() *Cluster {
	cs := fake.NewSimpleClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"}})
	return &Cluster{
		clientset: cs,
		factory:   informers.NewSharedInformerFactory(cs, 0),
		stopCh:    make(chan struct{}),
		events:    make(chan ResourceChangedMsg, 64),
		health:    newHealth(),
	}
}

// TestKindSyncedFalseBeforeRegistration: a real kind whose informer has not
// been registered has, by definition, an empty cache that means nothing. It
// must not read as "trustworthy empty".
func TestKindSyncedFalseBeforeRegistration(t *testing.T) {
	t.Parallel()
	c := newSyncTestCluster()
	defer close(c.stopCh)

	if c.KindSynced(KindSecret) {
		t.Fatal("KindSynced(Secret) = true before its informer was registered")
	}
}

func TestKindSyncedTrueAfterCacheFills(t *testing.T) {
	t.Parallel()
	c := newSyncTestCluster()
	defer close(c.stopCh)

	c.registerWatches(KindPod)
	c.factory.Start(c.stopCh)
	c.factory.WaitForCacheSync(c.stopCh)

	if !c.KindSynced(KindPod) {
		t.Fatal("KindSynced(Pod) = false after WaitForCacheSync")
	}
	// Registering one kind must not vouch for another.
	if c.KindSynced(KindSecret) {
		t.Fatal("KindSynced(Secret) = true after only Pod was registered")
	}
}

// TestKindSyncedTrueForSyntheticKinds: kinds with no informer at all have no
// cache to wait on, so gating a loading state on them would hang forever.
func TestKindSyncedTrueForSyntheticKinds(t *testing.T) {
	t.Parallel()
	c := newSyncTestCluster()
	defer close(c.stopCh)

	// Forwards are in-process state, not a cache.
	if !c.KindSynced(KindForward) {
		t.Error("KindSynced(Forward) = false; a kind with no informer has nothing to wait for")
	}
}

// TestKindSyncedForHelmReleasesTracksItsOwnCache: releases are no longer
// derived from the shared Secret cache — they have their own filtered
// informer, and this must answer for that one. Answering for KindSecret
// would report "settled" off a cache the Helm screens never read.
func TestKindSyncedForHelmReleasesTracksItsOwnCache(t *testing.T) {
	t.Parallel()
	c := newSyncTestCluster()
	defer close(c.stopCh)

	if c.KindSynced(KindHelmRelease) {
		t.Fatal("KindSynced(HelmRelease) = true before its informer was started")
	}

	// Starting the shared Secret cache must not vouch for releases.
	c.registerWatches(KindSecret)
	c.factory.Start(c.stopCh)
	c.factory.WaitForCacheSync(c.stopCh)
	if c.KindSynced(KindHelmRelease) {
		t.Fatal("the shared Secret cache wrongly vouched for HelmRelease")
	}

	if _, err := c.ListHelmReleaseSecrets(context.Background(), ""); err != nil {
		t.Fatalf("ListHelmReleaseSecrets: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !c.KindSynced(KindHelmRelease) && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !c.KindSynced(KindHelmRelease) {
		t.Fatal("KindSynced(HelmRelease) never settled after its own cache filled")
	}
}

// TestKindSyncedTrueAfterStop: a torn-down cluster will never sync anything
// again, so callers must be told to render what they have rather than spin.
func TestKindSyncedTrueAfterStop(t *testing.T) {
	t.Parallel()
	c := newSyncTestCluster()
	c.Stop()

	if !c.KindSynced(KindSecret) {
		t.Fatal("KindSynced(Secret) = false after Stop; a stopped cluster never syncs")
	}
}

// TestKindSyncedTrueAfterPermissionError is the anti-hang guard: an informer
// for a kind the user may not list never syncs, so without this browse would
// retry every 250ms forever instead of showing the permission-denied card.
func TestKindSyncedTrueAfterPermissionError(t *testing.T) {
	t.Parallel()
	c := newSyncTestCluster()
	defer close(c.stopCh)

	if c.KindSynced(KindHorizontalPodAutoscaler) {
		t.Fatal("precondition: unregistered kind should not report synced")
	}
	c.markKindFailed(KindHorizontalPodAutoscaler)

	if !c.KindSynced(KindHorizontalPodAutoscaler) {
		t.Fatal("KindSynced = false after a permission failure; the cache will never arrive")
	}
	// A denial for one kind says nothing about any other.
	if c.KindSynced(KindSecret) {
		t.Fatal("markKindFailed(HPA) wrongly vouched for Secret")
	}
}

func TestPermissionErrorsAreDistinguishedFromOutages(t *testing.T) {
	t.Parallel()
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "horizontalpodautoscalers"}, "", errors.New("nope"))
	if !IsPermissionError(forbidden) {
		t.Fatal("a Forbidden status error must count as a permission error")
	}
	if IsPermissionError(errors.New("connect: connection refused")) {
		t.Fatal("a refused connection is an outage, not a permission error — it must stay retryable")
	}
}

// TestSyncedIsALatch pins Synced's redefined meaning: it answers "has this
// cluster finished connecting", so a kind registered later must not drag it
// back to false.
func TestSyncedIsALatch(t *testing.T) {
	t.Parallel()
	c := newSyncTestCluster()
	defer close(c.stopCh)

	c.mu.Lock()
	c.synced = true
	c.mu.Unlock()

	c.registerWatches(KindSecret) // registered, definitely not filled
	if !c.Synced() {
		t.Fatal("Synced() went false after a later registration; it is a connect latch, not a cache state")
	}
	if c.KindSynced(KindSecret) {
		t.Fatal("KindSynced(Secret) = true for a registered-but-unstarted informer")
	}
}
