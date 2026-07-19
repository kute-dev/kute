package kube

import (
	"context"
	"fmt"
	"reflect"
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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// defaultResync is the informer resync period; watch events drive updates, so
// this only backstops missed notifications.
const defaultResync = 5 * time.Minute

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
		factory:    informers.NewSharedInformerFactory(client.Interface, defaultResync),
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

// Start registers watch handlers, starts the informers, and blocks until the
// caches for the registered kinds have synced (or ctx is done).
func (c *Cluster) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.mu.Unlock()

	c.registerWatches()
	c.factory.Start(c.stopCh)
	c.ensureDynamicKind(KindCustomResourceDefinition, crdGVR, false)
	go c.startHealthLoop(c.stopCh)

	// Wait for caches to sync, but abort if the caller cancels first. The
	// dynamic factory's sync (currently just the CRD informer) is folded
	// into the same bounded wait, but its result is never fatal — an
	// absent/unreachable apiextensions API just means CRD support degrades
	// to "no custom kinds," not a broken connection (refreshDiscovery
	// below tolerates an empty/still-syncing cache).
	var results map[reflect.Type]bool
	done := make(chan struct{})
	go func() {
		results = c.factory.WaitForCacheSync(c.stopCh)
		c.dynFactory.WaitForCacheSync(c.stopCh)
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	for _, synced := range results {
		if !synced {
			return fmt.Errorf("informer caches failed to sync")
		}
	}
	c.mu.Lock()
	c.synced = true
	c.mu.Unlock()

	c.refreshDiscovery(ctx)
	return nil
}

// Synced reports whether the informer caches have completed their initial
// sync. ListRaw reads the caches directly regardless of sync state, so right
// after launch (or mid SwitchContext) it can return a truthful-looking empty
// list before the first real objects have arrived; callers use Synced to
// tell that apart from a genuinely empty result.
func (c *Cluster) Synced() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.synced
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
	c.factory = informers.NewSharedInformerFactory(client.Interface, defaultResync)
	c.dynClient = dynClient
	c.dynFactory = dynamicinformer.NewDynamicSharedInformerFactory(dynClient, defaultResync)
	c.dynKinds = nil
	c.discovered = nil
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
	sel := labels.Everything()
	f := c.factory
	switch kind {
	case KindPod:
		return listNamespaced(f.Core().V1().Pods().Lister().List, f.Core().V1().Pods().Lister().Pods, namespace, sel)
	case KindService:
		return listNamespaced(f.Core().V1().Services().Lister().List, f.Core().V1().Services().Lister().Services, namespace, sel)
	case KindIngress:
		return listNamespaced(f.Networking().V1().Ingresses().Lister().List, f.Networking().V1().Ingresses().Lister().Ingresses, namespace, sel)
	case KindConfigMap:
		return listNamespaced(f.Core().V1().ConfigMaps().Lister().List, f.Core().V1().ConfigMaps().Lister().ConfigMaps, namespace, sel)
	case KindSecret:
		return listNamespaced(f.Core().V1().Secrets().Lister().List, f.Core().V1().Secrets().Lister().Secrets, namespace, sel)
	case KindPersistentVolumeClaim:
		return listNamespaced(f.Core().V1().PersistentVolumeClaims().Lister().List, f.Core().V1().PersistentVolumeClaims().Lister().PersistentVolumeClaims, namespace, sel)
	case KindEvent:
		return listNamespaced(f.Core().V1().Events().Lister().List, f.Core().V1().Events().Lister().Events, namespace, sel)
	case KindDeployment:
		return listNamespaced(f.Apps().V1().Deployments().Lister().List, f.Apps().V1().Deployments().Lister().Deployments, namespace, sel)
	case KindDaemonSet:
		return listNamespaced(f.Apps().V1().DaemonSets().Lister().List, f.Apps().V1().DaemonSets().Lister().DaemonSets, namespace, sel)
	case KindStatefulSet:
		return listNamespaced(f.Apps().V1().StatefulSets().Lister().List, f.Apps().V1().StatefulSets().Lister().StatefulSets, namespace, sel)
	case KindReplicaSet:
		return listNamespaced(f.Apps().V1().ReplicaSets().Lister().List, f.Apps().V1().ReplicaSets().Lister().ReplicaSets, namespace, sel)
	case KindJob:
		return listNamespaced(f.Batch().V1().Jobs().Lister().List, f.Batch().V1().Jobs().Lister().Jobs, namespace, sel)
	case KindCronJob:
		return listNamespaced(f.Batch().V1().CronJobs().Lister().List, f.Batch().V1().CronJobs().Lister().CronJobs, namespace, sel)
	case KindRole:
		return listNamespaced(f.Rbac().V1().Roles().Lister().List, f.Rbac().V1().Roles().Lister().Roles, namespace, sel)
	case KindRoleBinding:
		return listNamespaced(f.Rbac().V1().RoleBindings().Lister().List, f.Rbac().V1().RoleBindings().Lister().RoleBindings, namespace, sel)
	case KindClusterRole:
		items, err := f.Rbac().V1().ClusterRoles().Lister().List(sel)
		return toObjects(items), err
	case KindClusterRoleBinding:
		items, err := f.Rbac().V1().ClusterRoleBindings().Lister().List(sel)
		return toObjects(items), err
	case KindNode:
		items, err := f.Core().V1().Nodes().Lister().List(sel)
		return toObjects(items), err
	case KindNamespace:
		items, err := f.Core().V1().Namespaces().Lister().List(sel)
		return toObjects(items), err
	default:
		if info, ok := c.getDynKind(kind); ok {
			return listDynamic(info, namespace)
		}
		return nil, fmt.Errorf("no informer registered for kind %s", kind)
	}
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
func (c *Cluster) CountInstances(kind ResourceKind) int {
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

func toObjects[T runtime.Object](items []T) []runtime.Object {
	out := make([]runtime.Object, 0, len(items))
	for _, it := range items {
		out = append(out, it)
	}
	return out
}
