package resources

import "github.com/kute-dev/kute/internal/kube"

// Registry maps resource kinds to their descriptors.
type Registry struct {
	byKind map[kube.ResourceKind]Descriptor
}

// Descriptor returns the descriptor for kind, if registered.
func (r Registry) Descriptor(kind kube.ResourceKind) (Descriptor, bool) {
	d, ok := r.byKind[kind]
	return d, ok
}

// Has reports whether kind is registered.
func (r Registry) Has(kind kube.ResourceKind) bool {
	_, ok := r.byKind[kind]
	return ok
}

// Register adds or replaces d's entry — how discovered kinds (14a) and the
// CustomResourceDefinition list (14b) join a DefaultRegistry() base at
// connect/switch time (see BuildDiscoveredRegistry in crd.go). byKind is a
// map (reference type), so this mutates in place even though Registry
// itself is passed by value everywhere else.
func (r Registry) Register(d Descriptor) {
	if d.Health == nil {
		d.Health = StatusHealth
	}
	if d.HealthLabel == nil {
		d.HealthLabel = DefaultHealthLabel
	}
	r.byKind[d.Kind] = d
}

// DefaultRegistry returns the built-in descriptors for every supported kind.
// Adding a resource type is a single entry here plus its Project function.
func DefaultRegistry() Registry {
	descriptors := []Descriptor{
		{Kind: kube.KindPod, Group: GroupWorkloads, Display: "Pods", Icon: "◈", Columns: []string{"Name", "Ready", "Status", "Restarts", "CPU", "MEM", "Node", "Age"}, Describe: "running application instances", Project: projectPod, HealthLabel: podHealthLabel},
		{Kind: kube.KindDeployment, Group: GroupWorkloads, Display: "Deployments", Icon: "◈", Columns: []string{"Name", "Ready", "Rollout", "Image", "Age"}, Describe: "declarative pod rollouts", Project: projectDeployment(nil), HealthLabel: deploymentHealthLabel},
		{Kind: kube.KindDaemonSet, Group: GroupWorkloads, Display: "DaemonSets", Icon: "◈", Columns: []string{"Name", "Ready", "Available", "Age"}, Describe: "one pod per matching node", Project: projectDaemonSet},
		{Kind: kube.KindStatefulSet, Group: GroupWorkloads, Display: "StatefulSets", Icon: "◈", Columns: []string{"Name", "Ready", "Age"}, Describe: "stable, ordered pod identities", Project: projectStatefulSet},
		{Kind: kube.KindReplicaSet, Group: GroupWorkloads, Display: "ReplicaSets", Icon: "◈", Columns: []string{"Name", "Ready", "Replicas", "Age"}, Describe: "pod replica sets behind a deployment", Project: projectReplicaSet},
		{Kind: kube.KindJob, Group: GroupWorkloads, Display: "Jobs", Icon: "◈", Columns: []string{"Name", "Completions", "Active", "Age"}, Describe: "run-to-completion pods", Project: projectJob},
		{Kind: kube.KindCronJob, Group: GroupWorkloads, Display: "CronJobs", Icon: "◷", Columns: []string{"Name", "Schedule", "Suspend", "Active", "Age"}, Describe: "jobs run on a schedule", Project: projectCronJob},
		{Kind: kube.KindService, Group: GroupNetworking, Display: "Services", Icon: "◇", Columns: []string{"Name", "Type", "ClusterIP", "Ports", "Age"}, Describe: "stable network endpoints", Project: projectService},
		{Kind: kube.KindIngress, Group: GroupNetworking, Display: "Ingresses", Icon: "◇", Columns: []string{"Name", "Class", "Hosts", "TLS", "Backends", "Age"}, Describe: "external HTTP(S) routing rules", Project: projectIngress(nil)},
		{Kind: kube.KindConfigMap, Group: GroupConfig, Display: "ConfigMaps", Icon: "⚙", Columns: []string{"Name", "Data", "Age"}, Describe: "non-secret configuration data", Project: projectConfigMap},
		{Kind: kube.KindSecret, Group: GroupConfig, Display: "Secrets", Icon: "⚙", Columns: []string{"Name", "Type", "Data", "Age"}, Describe: "sensitive configuration data", Project: projectSecret},
		{Kind: kube.KindPersistentVolumeClaim, Group: GroupStorage, Display: "PersistentVolumeClaims", Icon: "▤", Columns: []string{"Name", "Status", "Capacity", "Age"}, Describe: "requested persistent storage", Project: projectPVC},
		{Kind: kube.KindNode, Group: GroupCluster, Display: "Nodes", Icon: "⬡", Columns: []string{"Name", "Status", "Pods", "CPU", "MEM", "Version", "Age"}, Describe: "cluster worker machines", ClusterScoped: true, Project: projectNode, HealthLabel: nodeHealthLabel},
		{Kind: kube.KindNamespace, Group: GroupCluster, Display: "Namespaces", Icon: "⬡", Columns: []string{"Name", "Status", "Age"}, Describe: "cluster resource partitions", ClusterScoped: true, Project: projectNamespace},
		{Kind: kube.KindEvent, Group: GroupObservability, Display: "Events", Icon: "∿", Columns: []string{"Type", "Reason", "Object", "Age"}, Describe: "cluster activity and warnings", Project: projectEvent},
		{Kind: kube.KindForward, Group: GroupCluster, Display: "Forwards", Icon: "⇄", Columns: []string{"Local", "Target", "Namespace", "Uptime", "Traffic"}, FlexColumn: "Target", Describe: "active kubectl port-forward sessions", ClusterScoped: true, Project: projectForward, HealthLabel: forwardHealthLabel},
		{Kind: kube.KindHelmRelease, Group: GroupCluster, Display: "Helm Releases", Icon: "⎈", Columns: []string{"Release", "Chart", "App Ver", "Rev", "Status", "Updated"}, FlexColumn: "Release", Describe: "deployed Helm chart releases", Project: projectHelmRelease, HealthLabel: helmReleaseHealthLabel},
	}
	// CustomResourceDefinition (14b) is always present, like Forward — a
	// built-in, not a discovered kind — registered here with a nil counter
	// (every row's COUNT reads 0) so DefaultGroups()/DefaultRegistry() stay
	// mutually consistent before any connect has happened.
	// BuildDiscoveredRegistry re-registers it with a live InstanceCounter
	// once a cluster is reachable.
	descriptors = append(descriptors, CRDDescriptor(nil))

	byKind := make(map[kube.ResourceKind]Descriptor, len(descriptors))
	for _, d := range descriptors {
		if d.Health == nil {
			d.Health = StatusHealth
		}
		if d.HealthLabel == nil {
			d.HealthLabel = DefaultHealthLabel
		}
		byKind[d.Kind] = d
	}
	return Registry{byKind: byKind}
}

// podHealthLabel is the docs/design/README.md 2a health-strip wording:
// "32 running", "2 pending", "1 crashloop", "1 completed".
func podHealthLabel(class StatusClass) string {
	switch class {
	case StatusOK:
		return "running"
	case StatusWarn:
		return "pending"
	case StatusFail:
		return "crashloop"
	default:
		return "completed"
	}
}

// deploymentHealthLabel is 9a's ROLLOUT-column wording reused for the
// health strip, so both agree on what "stable"/"progressing"/"degraded"
// mean for a Deployment (docs/design README.md §9a).
func deploymentHealthLabel(class StatusClass) string {
	switch class {
	case StatusOK:
		return "stable"
	case StatusWarn:
		return "progressing"
	case StatusFail:
		return "degraded"
	default:
		return "other"
	}
}

// forwardHealthLabel is 13c's health-strip wording: "3 active · 1
// reconnecting".
func forwardHealthLabel(class StatusClass) string {
	switch class {
	case StatusOK:
		return "active"
	case StatusWarn:
		return "reconnecting"
	default:
		return "other"
	}
}

// helmReleaseHealthLabel is 18a's health-strip wording: "3 deployed · 1
// pending-upgrade · 1 failed".
func helmReleaseHealthLabel(class StatusClass) string {
	switch class {
	case StatusOK:
		return "deployed"
	case StatusWarn:
		return "pending-upgrade"
	case StatusFail:
		return "failed"
	default:
		return "other"
	}
}

// nodeHealthLabel is the docs/design/README.md §11a health-strip wording:
// "3 ready · 1 pressure · 1 cordoned".
func nodeHealthLabel(class StatusClass) string {
	switch class {
	case StatusOK:
		return "ready"
	case StatusWarn:
		return "pressure"
	case StatusFail:
		return "not ready"
	default:
		return "cordoned"
	}
}
