package kube

import (
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// defaultResync is the informer resync period; watch events drive updates, so
// this only backstops missed notifications.
const defaultResync = 5 * time.Minute

// newTypedFactory builds the shared informer factory every typed cache comes
// from. The one construction site, so the cache transform can't be wired
// into production and quietly missed by a test's hand-built factory.
func newTypedFactory(client kubernetes.Interface) informers.SharedInformerFactory {
	return informers.NewSharedInformerFactoryWithOptions(client, defaultResync,
		informers.WithTransform(stripManagedFields))
}

// Cluster is the live data layer: a clientset plus a shared informer factory
// (so reads hit in-memory caches, not the API server) and a metrics client. It
// implements the resources.RawLister contract via ListRaw and emits change
// events for watched kinds. It is the informer/watch replacement for the
// one-shot Client* readers.
type Cluster struct {
	clientset kubernetes.Interface
	metrics   metricsclient.Interface
	factory   informers.SharedInformerFactory
	restCfg   *rest.Config
	Context   Context

	// dynClient/dynFactory back CRD discovery and every discovered kind's
	// list/watch (discovery.go/dynamic.go): a second, generic informer
	// mechanism alongside factory's typed one, since Pod/Deployment/…
	// have compile-time listers but a CRD's shape is only known at
	// runtime. dynKinds maps a ResourceKind (built-in
	// KindCustomResourceDefinition, or a discovered kind's own Kind name)
	// to its GVR/lister; discovered is the last refreshDiscovery pass's
	// parsed CRD cache (docs/design README.md's "discovery" state entry).
	dynClient  dynamic.Interface
	dynFactory dynamicinformer.DynamicSharedInformerFactory
	dynKinds   map[ResourceKind]dynamicKindInfo
	discovered []DiscoveredKind
	// crdColumnsFetched marks kinds whose printer columns have been pulled
	// from their own CRD. Discovery deliberately skips them (they live in
	// the part of a CRD that is megabytes), so they arrive per-kind on
	// first use — see ensurePrinterColumns.
	crdColumnsFetched map[ResourceKind]bool
	// crdColumnsInFlight dedupes concurrent fetches: browse re-reads on
	// every change event, and each read would otherwise launch its own.
	crdColumnsInFlight map[ResourceKind]bool

	// metaClient is CountLive's PartialObjectMetadata client, built on first
	// use and dropped on SwitchContext along with everything else bound to
	// the old cluster.
	metaClient metadata.Interface

	// helmFactories/helmInformers back KindHelmRelease: Secret informers of
	// their own, filtered server-side to type=helm.sh/release.v1 so listing
	// releases doesn't pull every Secret in the cluster (see helm.go's
	// ensureHelmSecrets). Started on first read, like every other lazy kind,
	// and keyed by namespace ("" = all) because a cluster-wide release list
	// is a much bigger read than the one namespace a screen is showing.
	// helmScope is the namespace of the most recent read, which is the cache
	// KindSynced(KindHelmRelease) has to answer for — a screen asking "is my
	// answer trustworthy yet" means the cache its own list just came from.
	helmFactories map[string]informers.SharedInformerFactory
	helmInformers map[string]cache.SharedIndexInformer
	helmScope     string

	// kindInformers is every typed informer registered so far, the handle
	// KindSynced needs (a lister can't report its own sync state).
	// kindFailed marks kinds whose watch hit a permanent error, so their
	// caches are never waited on again.
	kindInformers map[ResourceKind]cache.SharedIndexInformer
	kindFailed    map[ResourceKind]bool

	events  chan ResourceChangedMsg
	health  *health
	stopCh  chan struct{}
	started bool
	synced  bool
	mu      sync.Mutex
}

// dynamicKindInfo is one dynamically registered kind's watch/list handle.
type dynamicKindInfo struct {
	gvr        schema.GroupVersionResource
	namespaced bool
	lister     cache.GenericLister
	// informer is retained alongside the lister purely for HasSynced — a
	// lister can't report whether its cache has finished its initial fill.
	informer cache.SharedIndexInformer
}

// NewCluster builds a Cluster from the active kubeconfig (same resolution as
// NewClient). The metrics client is best-effort: a nil metrics client just
// yields empty usage rather than an error.
func NewCluster() (*Cluster, error) {
	return NewClusterForContext("")
}

// NewClusterForContext builds a Cluster pinned to the named kubeconfig
// context (same resolution as NewClientForContext), or the kubeconfig's
// current-context when contextName is "" — the seam BuildSession uses to
// restore the most-recently-used context at startup instead of always
// deferring to the kubeconfig file's own current-context.
func NewClusterForContext(contextName string) (*Cluster, error) {
	client, err := NewClientForContext(contextName)
	if err != nil {
		return nil, err
	}
	metrics, _ := metricsclient.NewForConfig(client.RESTConfig)
	dynClient, _ := dynamic.NewForConfig(client.RESTConfig)

	return &Cluster{
		clientset:  client.Interface,
		metrics:    metrics,
		factory:    newTypedFactory(client.Interface),
		restCfg:    client.RESTConfig,
		Context:    client.Context,
		dynClient:  dynClient,
		dynFactory: dynamicinformer.NewDynamicSharedInformerFactory(dynClient, defaultResync),
		events:     make(chan ResourceChangedMsg, 64),
		health:     newHealth(),
		stopCh:     make(chan struct{}),
	}, nil
}

// Clientset exposes the underlying clientset for callers that still need direct
// access (e.g. the existing one-shot pod lister and log streamer).
func (c *Cluster) Clientset() kubernetes.Interface { return c.clientset }

// RESTConfig exposes the REST config for building auxiliary clients (e.g. the
// metrics reader used by the rich pods screen).
func (c *Cluster) RESTConfig() *rest.Config { return c.restCfg }

// Events is the stream of change notifications from watched informers.
func (c *Cluster) Events() <-chan ResourceChangedMsg { return c.events }

// eagerKinds are the only typed informers started at connect time; every
// other kind waits until something actually reads it (see ensureKind).
//
// Each earns its place by being needed before the user has navigated
// anywhere, or by having no reload path if it arrives late:
//
//   - Namespace backs the breadcrumb, the n palette and the empty-state
//     hints, and is a handful of tiny objects.
//   - Pod is the default landing kind and feeds nearly every other screen.
//   - Node is bounded by node count rather than workload count, and the Pods
//     health strip, the Nodes list, node detail and the overview all read it
//     without subscribing to Node changes — so a late arrival would leave a
//     stale count on screen rather than correcting itself.
//
// Deliberately absent: Secret, ConfigMap, Event, ReplicaSet and
// ControllerRevision, which between them were most of what a connect used to
// pull before drawing anything.
var eagerKinds = []ResourceKind{KindNamespace, KindPod, KindNode}

// Start registers the eager watch handlers, starts those informers, and
// blocks until their caches have synced (or ctx is done). Kinds outside
// eagerKinds are started later, on first read.
func (c *Cluster) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.registerWatchesLocked(eagerKinds...)
	c.factory.Start(c.stopCh)
	c.mu.Unlock()
	go c.startHealthLoop(c.stopCh)

	// Wait on the eager informers by name rather than calling
	// factory.WaitForCacheSync, which waits on whatever is started at the
	// moment it's called: a lazily-started heavy kind (browse's restored
	// kind, say) can race into that snapshot and drag connect-time back to
	// what this change exists to remove.
	//
	// No dynamic informer is waited on, because none is started here any
	// more — discovery reads the API directly (refreshDiscovery) and the
	// CRD informer starts only if the 14b CRDs list is opened.
	var synced bool
	done := make(chan struct{})
	go func() {
		synced = cache.WaitForCacheSync(c.stopCh, c.eagerHasSynced()...)
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	if !synced {
		return fmt.Errorf("informer caches failed to sync")
	}
	c.mu.Lock()
	c.synced = true
	c.mu.Unlock()

	c.refreshDiscovery(ctx)
	return nil
}

// eagerHasSynced collects the HasSynced funcs of the eager set as registered.
func (c *Cluster) eagerHasSynced() []cache.InformerSynced {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]cache.InformerSynced, 0, len(eagerKinds))
	for _, kind := range eagerKinds {
		if inf, ok := c.kindInformers[kind]; ok {
			out = append(out, inf.HasSynced)
		}
	}
	return out
}

// ensureKind idempotently registers and starts kind's typed informer, the
// typed counterpart of ensureDynamicKind. ListRaw calls it before every
// read, which is what makes laziness invisible to callers: no screen has to
// declare what it needs, and a kind nobody opens is never watched.
//
// The whole body holds c.mu, for the same two reasons ensureDynamicKind
// does. It keeps the factory/stopCh reads from racing SwitchContext's
// replacement of both. And it makes registration-then-Start atomic: were the
// lock dropped in between, a concurrent caller's Start could run this
// informer before its watch-error handler was attached, and
// SetWatchErrorHandler then fails outright on an already-running informer.
func (c *Cluster) ensureKind(kind ResourceKind) {
	if _, ok := typedKinds[kind]; !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A stopped cluster has a nil stopCh, and an informer started with one
	// would never stop, so leave it unregistered.
	if c.stopCh == nil || c.kindInformers[kind] != nil {
		return
	}
	c.registerWatchesLocked(kind)
	c.factory.Start(c.stopCh)
	c.health.noteListBurst()
}

// Synced reports whether the startup informer set has completed its initial
// sync. It is a latch: set once at the end of Start and never cleared, so it
// answers "has this cluster finished connecting", not "is every cache
// currently up to date". Callers deciding whether a specific empty list is
// trustworthy want KindSynced instead.
func (c *Cluster) Synced() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.synced
}

// KindSynced reports whether kind's own informer cache has completed its
// initial fill. ListRaw reads caches directly regardless of sync state, so
// an empty result is ambiguous — genuinely no objects, or the cache hasn't
// been populated yet — and this is what disambiguates it.
//
// Per-kind rather than cluster-wide because each informer fills
// independently: the Namespace cache routinely has real data long before
// some unrelated, rarely-watched kind finishes, and on a cluster whose RBAC
// forbids listing (say) HorizontalPodAutoscalers, an aggregate flag never
// flips at all.
//
// Reports true — "this empty answer is trustworthy, render it" — for a
// stopped cluster, for synthetic kinds with no informer to wait on, and for
// a kind whose watch failed with a permission error, so a caller gating a
// loading state on this can never spin forever waiting for a cache that is
// never going to arrive.
func (c *Cluster) KindSynced(kind ResourceKind) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopCh == nil {
		return true
	}
	if c.kindFailed[kind] {
		return true
	}
	if kind == KindHelmRelease {
		// Releases come from their own filtered Secret cache, not the
		// shared one, so this must not answer for KindSecret — and there is
		// one such cache per namespace read, so it answers for the scope the
		// last read actually used.
		inf := c.helmInformers[c.helmScope]
		return inf != nil && inf.HasSynced()
	}
	if inf, ok := c.kindInformers[kind]; ok {
		return inf.HasSynced()
	}
	if info, ok := c.dynKinds[kind]; ok {
		return info.informer != nil && info.informer.HasSynced()
	}
	if _, typed := typedKinds[kind]; typed {
		// A real kind whose informer hasn't been registered yet.
		return false
	}
	return true
}

// allStartedKindsSynced reports whether every informer started so far has
// finished its initial LIST. This is what the health loop's connect-grace
// window keys off, rather than Synced: with informers starting on demand,
// the burst of LIST traffic that starves the /livez ping no longer happens
// only at connect time — opening the Secrets list on a constrained link
// produces exactly the same contention mid-session. Asking "is anything
// currently listing" covers both, where "have we connected yet" covers only
// the first.
func (c *Cluster) allStartedKindsSynced() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for kind, inf := range c.kindInformers {
		if !c.kindFailed[kind] && !inf.HasSynced() {
			return false
		}
	}
	for _, info := range c.dynKinds {
		if info.informer != nil && !info.informer.HasSynced() {
			return false
		}
	}
	if !c.kindFailed[KindHelmRelease] {
		for _, inf := range c.helmInformers {
			if !inf.HasSynced() {
				return false
			}
		}
	}
	return true
}

// markKindFailed records that kind's watch reported an error the cache will
// never recover from on its own — today only "you may not list this",
// which is a permanent answer for the session, not a transient outage.
func (c *Cluster) markKindFailed(kind ResourceKind) {
	c.mu.Lock()
	if c.kindFailed == nil {
		c.kindFailed = map[ResourceKind]bool{}
	}
	c.kindFailed[kind] = true
	c.mu.Unlock()
}

// CurrentNamespace and CurrentContext expose the active scope for switchers.
func (c *Cluster) CurrentNamespace() string { return c.Context.Namespace }
func (c *Cluster) CurrentContext() string   { return c.Context.ContextName }

// Contexts lists the kubeconfig context names available to switch to.
func (c *Cluster) Contexts() []string {
	names, _, err := AvailableContexts()
	if err != nil {
		return nil
	}
	return names
}

// SwitchNamespace changes the active namespace. Informers watch all namespaces,
// so this is a cheap filter change with no cache rebuild; ListRaw and the screens
// pick up the new scope on their next load.
func (c *Cluster) SwitchNamespace(namespace string) {
	if namespace == "" {
		return
	}
	c.mu.Lock()
	c.Context.Namespace = namespace
	c.mu.Unlock()
}

// SwitchContext rebuilds the clientset, metrics client, and informer factory
// against a different kubeconfig context, then restarts the informers. The
// events channel is preserved, so a caller already ranging over Events keeps
// receiving notifications from the new cluster. It blocks until the new caches
// sync (or ctx is done).
func (c *Cluster) SwitchContext(ctx context.Context, contextName string) error {
	client, err := NewClientForContext(contextName)
	if err != nil {
		return err
	}
	metrics, _ := metricsclient.NewForConfig(client.RESTConfig)
	dynClient, _ := dynamic.NewForConfig(client.RESTConfig)

	c.mu.Lock()
	// Tear down the current informers before swapping in the new factory.
	if c.stopCh != nil {
		close(c.stopCh)
	}
	c.stopCh = make(chan struct{})
	c.clientset = client.Interface
	c.metrics = metrics
	c.restCfg = client.RESTConfig
	c.factory = newTypedFactory(client.Interface)
	c.dynClient = dynClient
	c.dynFactory = dynamicinformer.NewDynamicSharedInformerFactory(dynClient, defaultResync)
	c.dynKinds = nil
	c.discovered = nil
	c.crdColumnsFetched = nil
	c.crdColumnsInFlight = nil
	// The new factory's informers are all unregistered and unsynced, and
	// whatever the old cluster forbade says nothing about this one.
	c.kindInformers = nil
	c.kindFailed = nil
	c.metaClient = nil
	c.helmFactories = nil
	c.helmInformers = nil
	c.helmScope = ""
	c.Context = client.Context
	c.health.reset()
	c.started = false
	c.synced = false
	c.mu.Unlock()

	return c.Start(ctx)
}

// Stop tears down the informers.
func (c *Cluster) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopCh != nil {
		close(c.stopCh)
		c.stopCh = nil
	}
}

// ListRaw returns the cached objects of kind in namespace ("" for all
// namespaces; ignored for cluster-scoped kinds). It satisfies
// resources.RawLister.
func (c *Cluster) ListRaw(_ context.Context, kind ResourceKind, namespace string) ([]runtime.Object, error) {
	if tk, ok := typedKinds[kind]; ok {
		// Start this kind's informer if nothing has yet. The first read
		// therefore returns an empty cache, which is exactly what
		// KindSynced is for — the caller sees "not synced", holds its
		// loading state, and the informer's own change events bring it
		// back once objects land.
		c.ensureKind(kind)
		// Snapshot the factory under the lock: SwitchContext replaces it
		// wholesale, so reading it unlocked could tear a read across two
		// clusters' caches.
		c.mu.Lock()
		f := c.factory
		c.mu.Unlock()
		return tk.list(f, namespace, labels.Everything())
	}
	c.ensureDynamicKindFor(kind)
	if info, ok := c.getDynKind(kind); ok {
		// Reading a custom kind is the moment its columns become worth
		// fetching. Fire-and-forget: this is on the update loop's path.
		c.ensurePrinterColumns(kind)
		return listDynamic(info, namespace)
	}
	return nil, fmt.Errorf("no informer registered for kind %s", kind)
}

// DiscoveredKinds returns the last discovery pass's parsed CRD cache
// (docs/design README.md's "discovery" state entry) — feeds
// resources.BuildDiscoveredRegistry.
func (c *Cluster) DiscoveredKinds() []DiscoveredKind {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]DiscoveredKind(nil), c.discovered...)
}

// CountInstances reads a dynamically registered kind's informer cache
// length — the 14b CRDs list's live COUNT column. 0 for a kind with no
// registered informer (not yet discovered, or discovery hasn't run).
//
// This starts the kind's instance informer if discovery knows it, so
// opening the CRDs list opens a watch per discovered kind. That's the one
// place laziness doesn't help: it's strictly better than the old behavior
// (which opened them all at connect, list or no list), but the column
// really wants a server-side count rather than a cache length.
func (c *Cluster) CountInstances(kind ResourceKind) int {
	c.ensureDynamicKindFor(kind)
	info, ok := c.getDynKind(kind)
	if !ok {
		return 0
	}
	objs, err := listDynamic(info, "")
	if err != nil {
		return 0
	}
	return len(objs)
}

// PodMetricsByNamespace fetches all pod metrics in namespace in a single List,
// replacing the previous per-pod Get (the N+1 loop). Keyed by pod name.
func (c *Cluster) PodMetricsByNamespace(ctx context.Context, namespace string) (map[string]PodMetrics, error) {
	if c.metrics == nil {
		return nil, fmt.Errorf("pod metrics client is not configured")
	}
	list, err := c.metrics.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]PodMetrics, len(list.Items))
	for i := range list.Items {
		pm := list.Items[i]
		if len(pm.Containers) == 0 {
			out[pm.Name] = PodMetrics{CPU: "n/a", MEM: "n/a"}
			continue
		}
		cpu := pm.Containers[0].Usage.Cpu().DeepCopy()
		mem := pm.Containers[0].Usage.Memory().DeepCopy()
		for j := 1; j < len(pm.Containers); j++ {
			cpu.Add(*pm.Containers[j].Usage.Cpu())
			mem.Add(*pm.Containers[j].Usage.Memory())
		}
		out[pm.Name] = PodMetrics{CPU: FormatCPU(cpu), MEM: FormatMemory(mem), CPUMilli: cpu.MilliValue(), MemBytes: mem.Value()}
	}
	return out, nil
}

// ContainerMetricsByNamespace fetches all pod metrics in namespace in a
// single List, like PodMetricsByNamespace, but keeps each container's own
// usage separate instead of summing them — 25a's per-field USAGE bar needs
// the active container's own number, not the whole pod's. Keyed by pod name
// then container name.
func (c *Cluster) ContainerMetricsByNamespace(ctx context.Context, namespace string) (map[string]map[string]PodMetrics, error) {
	if c.metrics == nil {
		return nil, fmt.Errorf("pod metrics client is not configured")
	}
	list, err := c.metrics.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]PodMetrics, len(list.Items))
	for i := range list.Items {
		pm := list.Items[i]
		containers := make(map[string]PodMetrics, len(pm.Containers))
		for _, ctr := range pm.Containers {
			cpu := ctr.Usage.Cpu()
			mem := ctr.Usage.Memory()
			containers[ctr.Name] = PodMetrics{
				CPU: FormatCPU(*cpu), MEM: FormatMemory(*mem),
				CPUMilli: cpu.MilliValue(), MemBytes: mem.Value(),
			}
		}
		out[pm.Name] = containers
	}
	return out, nil
}

// NodeMetrics fetches live CPU/MEM usage for every node in one List, keyed
// by node name — the 11a nodes-list bars' numerator. A nil metrics client
// (no metrics-server) reports the same "not configured" error
// PodMetricsByNamespace does, so callers degrade identically either way.
func (c *Cluster) NodeMetrics(ctx context.Context) (map[string]NodeMetric, error) {
	if c.metrics == nil {
		return nil, fmt.Errorf("node metrics client is not configured")
	}
	list, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]NodeMetric, len(list.Items))
	for i := range list.Items {
		nm := list.Items[i]
		cpu := nm.Usage.Cpu().DeepCopy()
		mem := nm.Usage.Memory().DeepCopy()
		out[nm.Name] = NodeMetric{CPU: FormatCPU(cpu), MEM: FormatMemory(mem), CPUMilli: cpu.MilliValue(), MemBytes: mem.Value()}
	}
	return out, nil
}

// listNamespaced dispatches to the cluster-wide or per-namespace lister. The
// generic signatures let one helper serve every namespaced kind.
func listNamespaced[T runtime.Object, N interface {
	List(labels.Selector) ([]T, error)
}](
	all func(labels.Selector) ([]T, error),
	scoped func(string) N,
	namespace string,
	sel labels.Selector,
) ([]runtime.Object, error) {
	if namespace == "" {
		items, err := all(sel)
		return toObjects(items), err
	}
	items, err := scoped(namespace).List(sel)
	return toObjects(items), err
}

// listAll is listNamespaced's cluster-scoped counterpart, for the kinds that
// have no per-namespace lister at all (Node, Namespace, ClusterRole,
// ClusterRoleBinding).
func listAll[T runtime.Object](all func(labels.Selector) ([]T, error), sel labels.Selector) ([]runtime.Object, error) {
	items, err := all(sel)
	return toObjects(items), err
}

func toObjects[T runtime.Object](items []T) []runtime.Object {
	out := make([]runtime.Object, 0, len(items))
	for _, it := range items {
		out = append(out, it)
	}
	return out
}
