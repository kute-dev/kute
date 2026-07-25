package kube

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	metadatafake "k8s.io/client-go/metadata/fake"
)

func newCountTestCluster(objs ...*metav1.PartialObjectMetadata) *Cluster {
	scheme := metadatafake.NewTestScheme()
	_ = metav1.AddMetaToScheme(scheme)
	runtimeObjs := make([]runtime.Object, len(objs))
	for i, o := range objs {
		runtimeObjs[i] = o
	}
	return &Cluster{metaClient: metadatafake.NewSimpleMetadataClient(scheme, runtimeObjs...)}
}

func partial(gvk schema.GroupVersionKind, namespace, name string) *metav1.PartialObjectMetadata {
	pom := &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	pom.SetGroupVersionKind(gvk)
	return pom
}

// TestCountLiveCountsWithoutAnInformer is the point of CountLive: the jump
// palette needs a number per kind, and reading those from informer caches
// would start a watch per kind the moment the palette opened.
func TestCountLiveCountsWithoutAnInformer(t *testing.T) {
	secretGVK := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
	c := newCountTestCluster(
		partial(secretGVK, "default", "a"),
		partial(secretGVK, "default", "b"),
		partial(secretGVK, "other", "c"),
	)

	n, err := c.CountLive(context.Background(), KindSecret, "default")
	if err != nil {
		t.Fatalf("CountLive: %v", err)
	}
	if n != 2 {
		t.Fatalf("CountLive(Secret, default) = %d, want 2", n)
	}

	// Counting must not have registered or started anything.
	c.mu.Lock()
	informers := len(c.kindInformers)
	c.mu.Unlock()
	if informers != 0 {
		t.Fatalf("CountLive started %d informers; it must not touch them at all", informers)
	}
}

func TestCountLiveIgnoresNamespaceForClusterScopedKinds(t *testing.T) {
	nodeGVK := schema.GroupVersionKind{Version: "v1", Kind: "Node"}
	c := newCountTestCluster(partial(nodeGVK, "", "node-1"), partial(nodeGVK, "", "node-2"))

	n, err := c.CountLive(context.Background(), KindNode, "default")
	if err != nil {
		t.Fatalf("CountLive: %v", err)
	}
	if n != 2 {
		t.Fatalf("CountLive(Node, \"default\") = %d, want 2 — Nodes have no namespace to filter by", n)
	}
}

func TestCountLiveUnknownKindErrors(t *testing.T) {
	c := newCountTestCluster()
	if _, err := c.CountLive(context.Background(), ResourceKind("Nonexistent"), ""); err == nil {
		t.Fatal("expected an error for a kind with no known API resource")
	}
}

// TestResourceForResolvesEveryTypedKind: CountLive addresses kinds by GVR
// rather than by lister, so every table entry has to resolve.
func TestResourceForResolvesEveryTypedKind(t *testing.T) {
	t.Parallel()
	c := &Cluster{}
	for kind := range typedKinds {
		gvr, _, ok := c.resourceFor(kind)
		if !ok || gvr.Resource == "" {
			t.Errorf("resourceFor(%s) = %v, %v; want a resolved GVR", kind, gvr, ok)
		}
	}
}
