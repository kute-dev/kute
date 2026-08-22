package kube

import (
	"sync/atomic"
	"testing"

	"k8s.io/client-go/tools/cache"
)

// countingInformer answers HasSynced from a settable flag and counts how
// often it was asked. Only HasSynced is ever called on it; the embedded nil
// interface makes any other call a loud panic rather than a silent pass.
type countingInformer struct {
	cache.SharedIndexInformer
	synced atomic.Bool
	calls  atomic.Int64
}

func (i *countingInformer) HasSynced() bool {
	i.calls.Add(1)
	return i.synced.Load()
}

// TestAllStartedKindsSyncedStopsSweepingOnceSynced pins the latch that keeps
// recordWatchError's c.mu hold O(1). Every reflector's watch error takes the
// same lock ListRaw/ensureKind take on the render loop's behalf, so a sweep
// over every started informer per error — with a flapping connection and
// thirty lazy informers started, exactly the situation this app is designed
// to reach — is UI jank by construction.
func TestAllStartedKindsSyncedStopsSweepingOnceSynced(t *testing.T) {
	c := &Cluster{health: newHealth()}
	pods := &countingInformer{}
	c.kindInformers = map[scopeKey]cache.SharedIndexInformer{{KindPod, ""}: pods}

	if c.allStartedKindsSynced() {
		t.Fatal("an unsynced informer must report not-all-synced")
	}
	if got := pods.calls.Load(); got != 1 {
		t.Fatalf("HasSynced called %d times, want 1", got)
	}

	pods.synced.Store(true)
	if !c.allStartedKindsSynced() {
		t.Fatal("a synced informer must report all-synced")
	}
	after := pods.calls.Load()

	for range 10 {
		if !c.allStartedKindsSynced() {
			t.Fatal("the latched answer flipped back to false on its own")
		}
	}
	if got := pods.calls.Load(); got != after {
		t.Fatalf("latched answer still swept the informers: %d extra HasSynced calls", got-after)
	}

	// A newly started informer has not synced, so the latch must not answer
	// for it. Getting this wrong hands the health loop's connect-grace
	// window the wrong answer for exactly the LIST burst it forgives.
	secrets := &countingInformer{}
	c.mu.Lock()
	c.kindInformers[scopeKey{KindSecret, ""}] = secrets
	c.noteInformerStartedLocked()
	c.mu.Unlock()

	if c.allStartedKindsSynced() {
		t.Fatal("starting an unsynced informer must clear the latch")
	}
	if secrets.calls.Load() == 0 {
		t.Fatal("the cleared latch never swept the new informer")
	}
}

// TestEveryInformerStartClearsTheSyncLatch covers the three registration
// sites — typed, dynamic and Helm — through the real ensure* paths rather
// than by inspecting the writes. A fourth site that forgets
// noteInformerStartedLocked fails silently: the informer starts, the stale
// latch keeps claiming everything is synced, and the connect grace closes
// over a LIST burst it should have forgiven.
func TestEveryInformerStartClearsTheSyncLatch(t *testing.T) {
	tests := []struct {
		name  string
		start func(c *Cluster)
	}{
		{"typed", func(c *Cluster) { c.ensureKind(KindReplicaSet, "") }},
		{"dynamic", func(c *Cluster) { c.ensureDynamicKind("CustomResourceDefinition", "", crdGVR, false) }},
		{"helm", func(c *Cluster) { c.ensureHelmSecrets("default") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newLazyTestCluster()
			defer c.Stop()
			if err := c.Start(t.Context()); err != nil {
				t.Fatalf("Start: %v", err)
			}

			c.mu.Lock()
			c.allKindsSynced = true
			c.mu.Unlock()

			tt.start(c)

			c.mu.Lock()
			latched := c.allKindsSynced
			c.mu.Unlock()
			if latched {
				t.Fatal("starting an informer left the all-synced latch standing")
			}
		})
	}
}
