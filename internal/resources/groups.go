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
