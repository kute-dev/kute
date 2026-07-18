package kube

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

// getDynKind/setDynKind guard dynKinds with the same mutex as every other
// Cluster field — refreshDiscovery/ensureDynamicKind run inside Start
// (background goroutine in app.RunWithConfig) while ListRaw is called from
// the Bubble Tea Update loop, so this map is genuinely cross-goroutine.
func (c *Cluster) getDynKind(kind ResourceKind) (dynamicKindInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, ok := c.dynKinds[kind]
	return info, ok
}

func (c *Cluster) setDynKind(kind ResourceKind, info dynamicKindInfo) {
	c.mu.Lock()
	if c.dynKinds == nil {
		c.dynKinds = map[ResourceKind]dynamicKindInfo{}
	}
	c.dynKinds[kind] = info
	c.mu.Unlock()
}

// ensureDynamicKind idempotently starts watching gvr under kind: the
// built-in KindCustomResourceDefinition (registered once in Start) or a
// discovered custom kind (registered by refreshDiscovery once its CRD is
// seen to be Established+Served). Safe to call repeatedly — dynFactory.Start
// only starts informers that haven't already been started.
func (c *Cluster) ensureDynamicKind(kind ResourceKind, gvr schema.GroupVersionResource, namespaced bool) {
	if _, ok := c.getDynKind(kind); ok {
		return
	}
	informer := c.dynFactory.ForResource(gvr)
	k := kind
	//nolint:errcheck // handler registration errors are non-fatal for a read-only UI
	_, _ = informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { c.notify(k) },
		UpdateFunc: func(any, any) { c.notify(k) },
		DeleteFunc: func(any) { c.notify(k) },
	})
	c.setDynKind(kind, dynamicKindInfo{gvr: gvr, namespaced: namespaced, lister: informer.Lister()})
	c.dynFactory.Start(c.stopCh)
}

// refreshDiscovery reads the (by now synced, best-effort) CRD informer
// cache, parses every CRD into a DiscoveredKind, and starts watching
// instances for every Established+Served one. Called once at the end of
// Start/SwitchContext — discovery is a per-context, per-connect snapshot
// (docs/design README.md §14c: "cached per context"), not continuously
// re-run mid-session.
func (c *Cluster) refreshDiscovery(_ context.Context) {
	info, ok := c.getDynKind(KindCustomResourceDefinition)
	if !ok {
		return
	}
	objs, err := listDynamic(info, "")
	if err != nil {
		return
	}
	discovered := make([]DiscoveredKind, 0, len(objs))
	for _, obj := range objs {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		dk, ok := ParseDiscoveredKind(u)
		if !ok {
			continue
		}
		discovered = append(discovered, dk)
		if dk.Established && dk.GVR.Version != "" {
			c.ensureDynamicKind(ResourceKind(dk.Kind), dk.GVR, !dk.ClusterScoped)
		}
	}
	c.mu.Lock()
	c.discovered = discovered
	c.mu.Unlock()
}

// listDynamic lists a dynamically registered kind's cache, scoped by
// namespace when the kind is namespaced and namespace is non-empty —
// mirrors listNamespaced's typed-lister dispatch for the generic
// cache.GenericLister shape.
func listDynamic(info dynamicKindInfo, namespace string) ([]runtime.Object, error) {
	if !info.namespaced || namespace == "" {
		return info.lister.List(labels.Everything())
	}
	return info.lister.ByNamespace(namespace).List(labels.Everything())
}
