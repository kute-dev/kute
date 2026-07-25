package kube

import (
	"context"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

// getDynKind guards dynKinds with the same mutex as every other Cluster
// field — refreshDiscovery/ensureDynamicKind run inside Start (background
// goroutine in app.RunWithConfig) while ListRaw is called from the Bubble
// Tea Update loop, so this map is genuinely cross-goroutine. There is no
// paired setter: ensureDynamicKind is the only writer and holds the lock
// across registration and Start, which a separate setter would break.
func (c *Cluster) getDynKind(kind ResourceKind) (dynamicKindInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, ok := c.dynKinds[kind]
	return info, ok
}

// ensureDynamicKind idempotently starts watching gvr under kind: the
// built-in KindCustomResourceDefinition (registered once in Start) or a
// discovered custom kind (registered by refreshDiscovery once its CRD is
// seen to be Established+Served). Safe to call repeatedly — dynFactory.Start
// only starts informers that haven't already been started.
//
// The whole body runs under c.mu, which does two things: it keeps the
// dynFactory/stopCh reads from racing SwitchContext's wholesale replacement
// of both, and it makes registration-then-Start atomic, so a concurrent
// caller's Start can't run this informer before its handlers are attached.
func (c *Cluster) ensureDynamicKind(kind ResourceKind, gvr schema.GroupVersionResource, namespaced bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.dynKinds[kind]; ok || c.stopCh == nil {
		return
	}
	informer := c.dynFactory.ForResource(gvr)
	// The dynamic factory takes no transform option, so set it per informer
	// — which must happen before it starts. Same reasoning as the typed
	// factory's: managedFields is bookkeeping nothing here reads.
	//nolint:errcheck // best-effort: a failure just means this cache keeps managedFields
	_ = informer.Informer().SetTransform(stripManagedFields)
	k := kind
	//nolint:errcheck // handler registration errors are non-fatal for a read-only UI
	_, _ = informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { c.notify(k) },
		UpdateFunc: func(any, any) { c.notify(k) },
		DeleteFunc: func(any) { c.notify(k) },
	})
	if c.dynKinds == nil {
		c.dynKinds = map[ResourceKind]dynamicKindInfo{}
	}
	c.dynKinds[kind] = dynamicKindInfo{
		gvr:        gvr,
		namespaced: namespaced,
		lister:     informer.Lister(),
		informer:   informer.Informer(),
	}
	c.dynFactory.Start(c.stopCh)
	c.health.noteListBurst()
}

// refreshDiscovery builds the per-context list of custom kinds (docs/design
// README.md §14c: "cached per context"). Called once at the end of
// Start/SwitchContext, not continuously re-run mid-session.
//
// It deliberately does not read CRD *objects*. A CustomResourceDefinition
// carries the full OpenAPI v3 validation schema for every version it serves,
// which on a cluster running a few operators is megabytes — 2.76 MB across 48
// CRDs on the cluster this was measured against, of which 1.8% was anything
// kute reads. Listing them to learn a handful of names was the single largest
// thing a connect pulled.
//
// Instead, two cheap reads that together say the same thing:
//
//   - a PartialObjectMetadata list of CRDs, which is just names — and a CRD's
//     name is exactly "<plural>.<group>", so it identifies precisely which
//     group/resource pairs are custom without any hardcoded list of built-in
//     API groups (a list that would get Gateway API wrong, since
//     gateway.networking.k8s.io is CRD-backed despite the k8s.io suffix);
//   - the discovery API, which supplies each one's Kind, served version and
//     scope.
//
// Printer columns are the one thing neither provides, and they only live in
// the part of a CRD that makes it huge. Those are fetched per-kind on first
// use — see ensurePrinterColumns.
func (c *Cluster) refreshDiscovery(ctx context.Context) {
	crdNames, err := c.listCRDNames(ctx)
	if err != nil || len(crdNames) == 0 {
		return
	}
	custom, err := c.discoverCustomResources(crdNames)
	if err != nil && len(custom) == 0 {
		return
	}
	c.mu.Lock()
	c.discovered = custom
	c.mu.Unlock()
}

// listCRDNames returns every CRD's metadata.name ("<plural>.<group>"),
// fetched as PartialObjectMetadata so the schemas never leave the server.
func (c *Cluster) listCRDNames(ctx context.Context) (map[string]bool, error) {
	client, err := c.metadataClient()
	if err != nil {
		return nil, err
	}
	list, err := client.Resource(crdGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(list.Items))
	for i := range list.Items {
		names[list.Items[i].Name] = true
	}
	return names, nil
}

// discoverCustomResources turns the discovery API's served-resource list into
// DiscoveredKinds, keeping only the resources whose "<name>.<group>" appears
// in crdNames. Established needs no separate check: a resource the API server
// is serving is, by definition, established.
//
// A partial-discovery error is tolerated and its usable half kept — one
// unreachable aggregated API service (a metrics adapter, typically) must not
// cost the user every other custom kind on the cluster.
func (c *Cluster) discoverCustomResources(crdNames map[string]bool) ([]DiscoveredKind, error) {
	_, lists, err := c.clientset.Discovery().ServerGroupsAndResources()
	if lists == nil {
		return nil, err
	}
	return customKindsFrom(lists, crdNames), err
}

// customKindsFrom is discoverCustomResources' pure half: match served
// resources against the CRD names and shape the survivors. Split out so the
// matching rules are testable without a discovery client.
func customKindsFrom(lists []*metav1.APIResourceList, crdNames map[string]bool) []DiscoveredKind {
	var out []DiscoveredKind
	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil || gv.Group == "" {
			continue
		}
		for _, r := range list.APIResources {
			// Subresources ("widgets/status") are not browsable kinds.
			if strings.Contains(r.Name, "/") || r.Kind == "" {
				continue
			}
			if !crdNames[r.Name+"."+gv.Group] {
				continue
			}
			out = append(out, DiscoveredKind{
				GVR:           schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: r.Name},
				Kind:          r.Kind,
				Plural:        r.Name,
				Group:         gv.Group,
				ClusterScoped: !r.Namespaced,
				Established:   true,
				CRDName:       r.Name + "." + gv.Group,
			})
		}
	}
	return dedupeDiscovered(out)
}

// dedupeDiscovered keeps one entry per Kind. A CRD serving several versions
// appears once per version in discovery; the preferred version sorts first
// within a group, so the first sighting wins.
func dedupeDiscovered(in []DiscoveredKind) []DiscoveredKind {
	seen := make(map[string]bool, len(in))
	out := make([]DiscoveredKind, 0, len(in))
	for _, dk := range in {
		if seen[dk.Kind] {
			continue
		}
		seen[dk.Kind] = true
		out = append(out, dk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// ensureDynamicKindFor starts the instance informer for a discovered kind by
// name, the dynamic counterpart of ensureKind. Reports whether kind is
// something discovery knows about at all — a false answer is what lets
// ListRaw tell "no such kind" apart from "not watched yet".
func (c *Cluster) ensureDynamicKindFor(kind ResourceKind) bool {
	c.mu.Lock()
	if _, ok := c.dynKinds[kind]; ok {
		c.mu.Unlock()
		return true
	}
	if kind == KindCustomResourceDefinition {
		// 14b's own list, the one screen that wants whole CRD objects —
		// schemas and all. Nothing else does, so this informer starts only
		// when that screen is opened, not at connect.
		c.mu.Unlock()
		c.ensureDynamicKind(KindCustomResourceDefinition, crdGVR, false)
		return true
	}
	var match DiscoveredKind
	var found bool
	for _, dk := range c.discovered {
		if ResourceKind(dk.Kind) == kind && dk.Established && dk.GVR.Version != "" {
			match, found = dk, true
			break
		}
	}
	c.mu.Unlock()
	if !found {
		return false
	}
	c.ensureDynamicKind(kind, match.GVR, !match.ClusterScoped)
	return true
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

// ensurePrinterColumns fetches one CRD's declared additionalPrinterColumns
// and folds them into the discovery cache, once per kind per context.
//
// Columns are the only thing refreshDiscovery can't get cheaply: they live in
// spec.versions[], the same part of a CRD that carries the megabytes of
// OpenAPI schema. So rather than pay that for every CRD at connect, one CRD
// is fetched the first time its kind is actually read — typically none per
// session, occasionally one or two.
//
// Until it lands the kind renders with neutral NAME/AGE columns, which
// §14a's spec already provides for ("printer columns → columns, Ready-style
// condition → status, else neutral"). Reports whether anything changed, so
// the caller only announces a registry rebuild when there's something new.
func (c *Cluster) ensurePrinterColumns(ctx context.Context, kind ResourceKind) bool {
	c.mu.Lock()
	if c.crdColumnsFetched[kind] {
		c.mu.Unlock()
		return false
	}
	var crdName string
	for _, dk := range c.discovered {
		if ResourceKind(dk.Kind) == kind {
			crdName = dk.CRDName
			break
		}
	}
	dyn := c.dynClient
	c.mu.Unlock()
	if crdName == "" || dyn == nil {
		return false
	}

	obj, err := dyn.Resource(crdGVR).Get(ctx, crdName, metav1.GetOptions{})
	if err != nil {
		// Leave it unfetched: a transient failure should retry on the next
		// read, and a permanent one (no permission to read CRDs) just means
		// this kind keeps its neutral columns.
		return false
	}
	full, ok := ParseDiscoveredKind(obj)
	if !ok {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.crdColumnsFetched == nil {
		c.crdColumnsFetched = map[ResourceKind]bool{}
	}
	c.crdColumnsFetched[kind] = true
	for i := range c.discovered {
		if ResourceKind(c.discovered[i].Kind) != kind {
			continue
		}
		// Only the fields the cheap discovery pass couldn't supply. GVR
		// stays as discovery reported it — that's the version the server
		// actually serves, which is what the instance informer must watch.
		c.discovered[i].PrinterColumns = full.PrinterColumns
		c.discovered[i].Versions = full.Versions
		return len(full.PrinterColumns) > 0
	}
	return false
}
