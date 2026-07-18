package kube

// ResourceKind identifies a Kubernetes resource type the UI can list. The string
// value is the API Kind, used for display and as the registry key.
type ResourceKind string

const (
	KindPod                   ResourceKind = "Pod"
	KindDeployment            ResourceKind = "Deployment"
	KindDaemonSet             ResourceKind = "DaemonSet"
	KindStatefulSet           ResourceKind = "StatefulSet"
	KindReplicaSet            ResourceKind = "ReplicaSet"
	KindJob                   ResourceKind = "Job"
	KindCronJob               ResourceKind = "CronJob"
	KindService               ResourceKind = "Service"
	KindIngress               ResourceKind = "Ingress"
	KindConfigMap             ResourceKind = "ConfigMap"
	KindSecret                ResourceKind = "Secret"
	KindPersistentVolumeClaim ResourceKind = "PersistentVolumeClaim"
	KindNode                  ResourceKind = "Node"
	KindNamespace             ResourceKind = "Namespace"
	KindEvent                 ResourceKind = "Event"
	// KindForward is a synthetic kind: not a Kubernetes API type, but a
	// registry entry for the app's own in-process port-forward sessions
	// (docs/design README.md §13c) — ForwardManager.ListRaw feeds it
	// through the same resources.List/Project pipeline as every real kind.
	KindForward ResourceKind = "Forward"
	// KindCustomResourceDefinition is the 14b CRDs list — a built-in kind
	// (like Forward) backed by a dynamic informer over
	// apiextensions.k8s.io/v1 rather than a typed one (discovery.go/
	// dynamic.go). Discovered custom kinds (14a) are registered under their
	// own ResourceKind(dk.Kind), not this one.
	KindCustomResourceDefinition ResourceKind = "CustomResourceDefinition"

	// KindHelmRelease is a synthetic kind, like Forward: not its own
	// Kubernetes API type, but a registry entry decoded from
	// helm.sh/release.v1 Secrets already sitting in the watched Secret cache
	// (docs/design README.md §18a, kube/helm.go). One row per release
	// (namespace+name), aggregated to its highest revision.
	KindHelmRelease ResourceKind = "HelmRelease"

	// Gateway API kinds (gateway.networking.k8s.io) are never DefaultRegistry
	// entries — like every CRD they only exist in the registry once
	// discovered (docs/design README.md §23b, CLAUDE.md's "CRD support is
	// data, not code"). These consts exist purely so browse can name them
	// when routing ↵ to the bespoke routing table (tasks/routetable) instead
	// of the generic 14d object detail — the same kind-name carve-out
	// Pod/Node/Deployment's own bespoke ↵ routing already takes.
	KindHTTPRoute ResourceKind = "HTTPRoute"
	KindGRPCRoute ResourceKind = "GRPCRoute"
	KindTCPRoute  ResourceKind = "TCPRoute"
	KindGateway   ResourceKind = "Gateway"

	// RBAC kinds (rbac.authorization.k8s.io/v1) back tasks/whocan's (22a)
	// cache-only binding resolution and the "↵ opens the binding's YAML"
	// verb — they're real informer-backed kinds like Pod/Node, just never
	// registered in resources.Registry (they have no browse list; whocan
	// is a query, not a browser).
	KindRole               ResourceKind = "Role"
	KindRoleBinding        ResourceKind = "RoleBinding"
	KindClusterRole        ResourceKind = "ClusterRole"
	KindClusterRoleBinding ResourceKind = "ClusterRoleBinding"

	// KindWhoCan is a synthetic kind, like KindForward: not a Kubernetes API
	// type and never registered in resources.Registry (there is nothing to
	// list — 22a is "a query, not a browser"). It exists purely so the goto
	// corpus (tui/goto.go) can surface a "who-can" jump result and browse's
	// switchKind (tui/tasks/browse/update.go) can recognize it as a
	// carve-out that pushes tasks/whocan directly, the same kind-name
	// special-case Ingress/Gateway routing already takes for
	// tasks/routetable.
	KindWhoCan ResourceKind = "WhoCan"

	// KindOverview is a synthetic kind, like KindWhoCan: not a Kubernetes API
	// type and never registered in resources.Registry — 19a is "a routing
	// layer, not a dashboard", so there's nothing to list. It exists purely
	// so the goto corpus can surface a `g "ov"` jump result and browse's
	// switchKind can recognize it as a carve-out that pushes
	// tasks/overview directly, the same kind-name special-case KindWhoCan
	// already takes.
	KindOverview ResourceKind = "Overview"
)

// ResourceChangedMsg is emitted (as a tea.Msg) when a watched informer observes
// an add/update/delete for a kind, so screens can re-read from the cache. It is
// a plain struct so the kube package needs no dependency on Bubble Tea.
type ResourceChangedMsg struct {
	Kind ResourceKind
}

// CRDsDiscoveredMsg fires once after the initial (detached, out-of-band)
// cluster.Start goroutine in app.RunWithConfig completes — the one connect
// path where CRD discovery finishes outside any tea.Cmd the root shell can
// await synchronously (context switch and reconnect both already run
// discovery inline before their own result message is returned). The root
// shell handles it by rebuilding Session.Registry/Groups from
// Cluster.DiscoveredKinds().
type CRDsDiscoveredMsg struct{}
