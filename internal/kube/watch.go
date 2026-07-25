package kube

import (
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// typedKind is one built-in kind's entry in the typedKinds table: how to
// reach its informer, how to read its cache, and enough API identity to
// address it without a cache at all.
//
// The informer and list funcs deliberately hang off the same factory rather
// than off a captured lister, because SwitchContext replaces c.factory
// wholesale — every call site passes the factory it just snapshotted under
// c.mu, so a stale factory can never leak into a read.
type typedKind struct {
	// gvr identifies the resource for clients that don't go through the
	// informer at all (the metadata client behind live counts).
	gvr schema.GroupVersionResource
	// clusterScoped kinds ignore the namespace argument entirely.
	clusterScoped bool
	// informer returns (and, as a side effect, registers) this kind's
	// shared informer on f.
	informer func(f informers.SharedInformerFactory) cache.SharedIndexInformer
	// list reads this kind's cache, scoped to namespace when it's
	// namespaced and namespace is non-empty.
	list func(f informers.SharedInformerFactory, namespace string, sel labels.Selector) ([]runtime.Object, error)
}

// typedKinds is the single source of truth for every kind backed by a typed
// informer: which informers exist, how each one's cache is read, and each
// one's API coordinates.
//
// It replaced a hand-maintained pairing of a handler map here and a parallel
// switch in ListRaw, which carried a "keep these in sync" comment. That
// pairing was merely tedious while informers were all started once at
// launch; it became a correctness trap the moment starting them became
// incremental. Calling a factory's Lister() *registers* that informer as a
// side effect, so a read through a path this table doesn't know about would
// silently enlist an informer that the next factory.Start() then runs with
// no watch-error handler attached. One table, one registration path.
var typedKinds = map[ResourceKind]typedKind{
	KindPod: {
		gvr:      schema.GroupVersionResource{Version: "v1", Resource: "pods"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Core().V1().Pods().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Core().V1().Pods().Lister()
			return listNamespaced(l.List, l.Pods, ns, sel)
		},
	},
	KindService: {
		gvr:      schema.GroupVersionResource{Version: "v1", Resource: "services"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Core().V1().Services().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Core().V1().Services().Lister()
			return listNamespaced(l.List, l.Services, ns, sel)
		},
	},
	KindIngress: {
		gvr:      schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Networking().V1().Ingresses().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Networking().V1().Ingresses().Lister()
			return listNamespaced(l.List, l.Ingresses, ns, sel)
		},
	},
	KindConfigMap: {
		gvr:      schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Core().V1().ConfigMaps().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Core().V1().ConfigMaps().Lister()
			return listNamespaced(l.List, l.ConfigMaps, ns, sel)
		},
	},
	KindSecret: {
		gvr:      schema.GroupVersionResource{Version: "v1", Resource: "secrets"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Core().V1().Secrets().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Core().V1().Secrets().Lister()
			return listNamespaced(l.List, l.Secrets, ns, sel)
		},
	},
	KindPersistentVolumeClaim: {
		gvr: schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer {
			return f.Core().V1().PersistentVolumeClaims().Informer()
		},
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Core().V1().PersistentVolumeClaims().Lister()
			return listNamespaced(l.List, l.PersistentVolumeClaims, ns, sel)
		},
	},
	KindEvent: {
		gvr:      schema.GroupVersionResource{Version: "v1", Resource: "events"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Core().V1().Events().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Core().V1().Events().Lister()
			return listNamespaced(l.List, l.Events, ns, sel)
		},
	},
	KindNode: {
		gvr:           schema.GroupVersionResource{Version: "v1", Resource: "nodes"},
		clusterScoped: true,
		informer:      func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Core().V1().Nodes().Informer() },
		list: func(f informers.SharedInformerFactory, _ string, sel labels.Selector) ([]runtime.Object, error) {
			return listAll(f.Core().V1().Nodes().Lister().List, sel)
		},
	},
	KindNamespace: {
		gvr:           schema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
		clusterScoped: true,
		informer:      func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Core().V1().Namespaces().Informer() },
		list: func(f informers.SharedInformerFactory, _ string, sel labels.Selector) ([]runtime.Object, error) {
			return listAll(f.Core().V1().Namespaces().Lister().List, sel)
		},
	},
	KindDeployment: {
		gvr:      schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Apps().V1().Deployments().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Apps().V1().Deployments().Lister()
			return listNamespaced(l.List, l.Deployments, ns, sel)
		},
	},
	KindDaemonSet: {
		gvr:      schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Apps().V1().DaemonSets().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Apps().V1().DaemonSets().Lister()
			return listNamespaced(l.List, l.DaemonSets, ns, sel)
		},
	},
	KindStatefulSet: {
		gvr:      schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Apps().V1().StatefulSets().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Apps().V1().StatefulSets().Lister()
			return listNamespaced(l.List, l.StatefulSets, ns, sel)
		},
	},
	KindReplicaSet: {
		gvr:      schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Apps().V1().ReplicaSets().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Apps().V1().ReplicaSets().Lister()
			return listNamespaced(l.List, l.ReplicaSets, ns, sel)
		},
	},
	KindControllerRevision: {
		gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "controllerrevisions"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer {
			return f.Apps().V1().ControllerRevisions().Informer()
		},
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Apps().V1().ControllerRevisions().Lister()
			return listNamespaced(l.List, l.ControllerRevisions, ns, sel)
		},
	},
	KindJob: {
		gvr:      schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Batch().V1().Jobs().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Batch().V1().Jobs().Lister()
			return listNamespaced(l.List, l.Jobs, ns, sel)
		},
	},
	KindCronJob: {
		gvr:      schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Batch().V1().CronJobs().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Batch().V1().CronJobs().Lister()
			return listNamespaced(l.List, l.CronJobs, ns, sel)
		},
	},
	KindHorizontalPodAutoscaler: {
		gvr: schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer {
			return f.Autoscaling().V2().HorizontalPodAutoscalers().Informer()
		},
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Autoscaling().V2().HorizontalPodAutoscalers().Lister()
			return listNamespaced(l.List, l.HorizontalPodAutoscalers, ns, sel)
		},
	},
	KindRole: {
		gvr:      schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Rbac().V1().Roles().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Rbac().V1().Roles().Lister()
			return listNamespaced(l.List, l.Roles, ns, sel)
		},
	},
	KindRoleBinding: {
		gvr:      schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"},
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Rbac().V1().RoleBindings().Informer() },
		list: func(f informers.SharedInformerFactory, ns string, sel labels.Selector) ([]runtime.Object, error) {
			l := f.Rbac().V1().RoleBindings().Lister()
			return listNamespaced(l.List, l.RoleBindings, ns, sel)
		},
	},
	KindClusterRole: {
		gvr:           schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
		clusterScoped: true,
		informer:      func(f informers.SharedInformerFactory) cache.SharedIndexInformer { return f.Rbac().V1().ClusterRoles().Informer() },
		list: func(f informers.SharedInformerFactory, _ string, sel labels.Selector) ([]runtime.Object, error) {
			return listAll(f.Rbac().V1().ClusterRoles().Lister().List, sel)
		},
	},
	KindClusterRoleBinding: {
		gvr:           schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"},
		clusterScoped: true,
		informer: func(f informers.SharedInformerFactory) cache.SharedIndexInformer {
			return f.Rbac().V1().ClusterRoleBindings().Informer()
		},
		list: func(f informers.SharedInformerFactory, _ string, sel labels.Selector) ([]runtime.Object, error) {
			return listAll(f.Rbac().V1().ClusterRoleBindings().Lister().List, sel)
		},
	},
}

// registerWatches attaches change handlers to each of kinds and, as a side
// effect of reaching for their informers, registers them with the factory so
// the next factory.Start runs them. Passing no kinds registers every kind in
// typedKinds.
func (c *Cluster) registerWatches(kinds ...ResourceKind) {
	if len(kinds) == 0 {
		kinds = make([]ResourceKind, 0, len(typedKinds))
		for kind := range typedKinds {
			kinds = append(kinds, kind)
		}
	}
	handlers := make(map[ResourceKind]cache.SharedIndexInformer, len(kinds))
	for _, kind := range kinds {
		tk, ok := typedKinds[kind]
		if !ok {
			continue
		}
		handlers[kind] = tk.informer(c.factory)
	}
	// Must run before factory.Start (SetWatchErrorHandler errors once an
	// informer is already running).
	c.setWatchErrorHandlers(handlers)
	for kind, informer := range handlers {
		kind := kind
		//nolint:errcheck // handler registration errors are non-fatal for a read-only UI
		_, _ = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    func(any) { c.notify(kind) },
			UpdateFunc: func(any, any) { c.notify(kind) },
			DeleteFunc: func(any) { c.notify(kind) },
		})
	}
	c.mu.Lock()
	if c.kindInformers == nil {
		c.kindInformers = make(map[ResourceKind]cache.SharedIndexInformer, len(handlers))
	}
	for kind, informer := range handlers {
		c.kindInformers[kind] = informer
	}
	c.mu.Unlock()
}

// notify delivers a change event without blocking the informer goroutine; if the
// buffer is full the event is dropped and the next resync/refresh reconciles.
func (c *Cluster) notify(kind ResourceKind) {
	select {
	case c.events <- ResourceChangedMsg{Kind: kind}:
	default:
	}
}
