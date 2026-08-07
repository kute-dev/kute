package resources

import "github.com/kute-dev/kute/internal/kube"

// GroupID identifies a resource group in the Home explorer's left panel.
type GroupID string

const (
	GroupWorkloads     GroupID = "Workloads"
	GroupNetworking    GroupID = "Networking"
	GroupConfig        GroupID = "Config"
	GroupStorage       GroupID = "Storage"
	GroupCluster       GroupID = "Cluster"
	GroupObservability GroupID = "Observability"
	// GroupCustomResources buckets discovered CRD kinds (14a) — appended by
	// BuildDiscoveredRegistry only when discovery found at least one, never
	// part of DefaultGroups' static list.
	GroupCustomResources GroupID = "Custom Resources"
	// GroupFlux buckets discovered Flux kinds (docs/design README.md §30a).
	// Like GroupCustomResources it is appended by BuildDiscoveredRegistry
	// only when discovery found one, never part of DefaultGroups' static
	// list — and the two are mutually exclusive: a kind is in exactly one
	// group. Sorts before Custom Resources, which stays last as the
	// catch-all long tail.
	GroupFlux GroupID = "Flux"
	// GroupArgo buckets the discovered Argo CD Application kind (docs/
	// design README.md §33a) — same appended-only-when-found, mutually
	// exclusive-with-Custom-Resources shape as GroupFlux. AppProject stays
	// in Custom Resources: it carries none of Application's sync/health
	// status, so it never earns the curated descriptor that would move it
	// here (see resources/crd.go's BuildDiscoveredRegistry).
	GroupArgo GroupID = "Argo CD"
)

// Group is a labelled bucket of resource kinds shown in the explorer.
type Group struct {
	ID    GroupID
	Icon  string
	Kinds []kube.ResourceKind
}

// DefaultGroups returns the explorer groups in display order. Icons match
// the 3a jump-palette taxonomy grid (docs/design/README.md): ◈ ◇ ⚙ ▤ ⬡ ∿.
func DefaultGroups() []Group {
	return []Group{
		{ID: GroupWorkloads, Icon: "◈", Kinds: []kube.ResourceKind{
			kube.KindPod, kube.KindDeployment, kube.KindDaemonSet, kube.KindStatefulSet,
			kube.KindReplicaSet, kube.KindJob, kube.KindCronJob,
		}},
		{ID: GroupNetworking, Icon: "◇", Kinds: []kube.ResourceKind{
			kube.KindService, kube.KindIngress,
		}},
		{ID: GroupConfig, Icon: "⚙", Kinds: []kube.ResourceKind{
			kube.KindConfigMap, kube.KindSecret,
		}},
		{ID: GroupStorage, Icon: "▤", Kinds: []kube.ResourceKind{
			kube.KindPersistentVolumeClaim,
		}},
		{ID: GroupCluster, Icon: "⬡", Kinds: []kube.ResourceKind{
			kube.KindNode, kube.KindNamespace, kube.KindForward, kube.KindCustomResourceDefinition, kube.KindHelmRelease,
		}},
		{ID: GroupObservability, Icon: "∿", Kinds: []kube.ResourceKind{
			kube.KindEvent,
		}},
	}
}
