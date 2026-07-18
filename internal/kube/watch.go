package kube

import "k8s.io/client-go/tools/cache"

// registerWatches attaches change handlers to every kind Cluster serves. Calling
// .Informer() also registers the informer with the factory, so this doubles as
// the "which informers to start" list — keep it in sync with ListRaw.
func (c *Cluster) registerWatches() {
	f := c.factory
	handlers := map[ResourceKind]cache.SharedIndexInformer{
		KindPod:                   f.Core().V1().Pods().Informer(),
		KindService:               f.Core().V1().Services().Informer(),
		KindIngress:               f.Networking().V1().Ingresses().Informer(),
		KindConfigMap:             f.Core().V1().ConfigMaps().Informer(),
		KindSecret:                f.Core().V1().Secrets().Informer(),
		KindPersistentVolumeClaim: f.Core().V1().PersistentVolumeClaims().Informer(),
		KindEvent:                 f.Core().V1().Events().Informer(),
		KindNode:                  f.Core().V1().Nodes().Informer(),
		KindNamespace:             f.Core().V1().Namespaces().Informer(),
		KindDeployment:            f.Apps().V1().Deployments().Informer(),
		KindDaemonSet:             f.Apps().V1().DaemonSets().Informer(),
		KindStatefulSet:           f.Apps().V1().StatefulSets().Informer(),
		KindReplicaSet:            f.Apps().V1().ReplicaSets().Informer(),
		KindJob:                   f.Batch().V1().Jobs().Informer(),
		KindCronJob:               f.Batch().V1().CronJobs().Informer(),
		KindRole:                  f.Rbac().V1().Roles().Informer(),
		KindRoleBinding:           f.Rbac().V1().RoleBindings().Informer(),
		KindClusterRole:           f.Rbac().V1().ClusterRoles().Informer(),
		KindClusterRoleBinding:    f.Rbac().V1().ClusterRoleBindings().Informer(),
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
}

// notify delivers a change event without blocking the informer goroutine; if the
// buffer is full the event is dropped and the next resync/refresh reconciles.
func (c *Cluster) notify(kind ResourceKind) {
	select {
	case c.events <- ResourceChangedMsg{Kind: kind}:
	default:
	}
}
