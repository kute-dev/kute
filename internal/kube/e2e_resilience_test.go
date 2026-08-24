//go:build e2e

package kube

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestInformerShapesKeepOneCacheAcrossWireChurn is the package-level
// companion to test/e2e/watch_recovery_test.go. That test controls WATCH/410
// ordering at the proxy; this one can inspect the unexported informer maps and
// proves the same typed/dynamic/filtered shapes retain exactly one cache.
func TestInformerShapesKeepOneCacheAcrossWireChurn(t *testing.T) {
	requireE2ECluster(t)
	c := e2eCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if _, err := c.ListRaw(ctx, KindConfigMap, e2eNamespace); err != nil {
		t.Fatal(err)
	}
	waitForKindSynced(t, c, KindConfigMap, "", 60*time.Second)

	widgetKind := ResourceKind("Widget")
	waitForDiscoveredKind(t, c, widgetKind, 60*time.Second)
	if _, err := c.ListRaw(ctx, widgetKind, e2eNamespace); err != nil {
		t.Fatal(err)
	}
	waitForKindSynced(t, c, widgetKind, "", 60*time.Second)

	if _, err := c.ListHelmReleaseSecrets(ctx, e2eNamespace); err != nil {
		t.Fatal(err)
	}
	waitForKindSynced(t, c, KindHelmRelease, e2eNamespace, 60*time.Second)

	c.mu.Lock()
	typed := c.kindInformers[scopeKey{KindConfigMap, ""}]
	dynamic := c.dynKinds[scopeKey{widgetKind, ""}].informer
	helm := c.helmInformers[e2eNamespace]
	c.mu.Unlock()
	if typed == nil || dynamic == nil || helm == nil {
		t.Fatalf("missing informer shape: typed=%v dynamic=%v helm=%v", typed, dynamic, helm)
	}

	name := fmt.Sprintf("wire-churn-%d", time.Now().UnixNano())
	_, err := c.clientset.CoreV1().ConfigMaps(e2eNamespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name}, Data: map[string]string{"marker": "wire"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = c.clientset.CoreV1().ConfigMaps(e2eNamespace).Delete(cleanupCtx, name, metav1.DeleteOptions{})
	})
	waitForCachedName(t, c, KindConfigMap, e2eNamespace, name, 30*time.Second)

	// Re-reading after a real server event must reuse, not replace, each
	// informer. The retained cache is what prevents a false-empty frame while
	// client-go relists after a watch expiration.
	for range 5 {
		_, _ = c.ListRaw(ctx, KindConfigMap, e2eNamespace)
		_, _ = c.ListRaw(ctx, widgetKind, e2eNamespace)
		_, _ = c.ListHelmReleaseSecrets(ctx, e2eNamespace)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if got := c.kindInformers[scopeKey{KindConfigMap, ""}]; got != typed {
		t.Fatal("typed informer was replaced")
	}
	if got := c.dynKinds[scopeKey{widgetKind, ""}].informer; got != dynamic {
		t.Fatal("dynamic informer was replaced")
	}
	if got := c.helmInformers[e2eNamespace]; got != helm {
		t.Fatal("filtered Helm informer was replaced")
	}
}

func waitForDiscoveredKind(t *testing.T, c *Cluster, kind ResourceKind, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, discovered := range c.DiscoveredKinds() {
			if discovered.RegistryKind() == kind {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("kind %s was never discovered", kind)
}
