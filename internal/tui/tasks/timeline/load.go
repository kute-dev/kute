package timeline

import (
	"context"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
)

// load fetches every source of 16a/16b's merged feed (Events, container
// restarts, Deployment rollout revisions) for the screen's scope and merges
// them into one newest-first clock (docs/design README.md §16a: "one clock,
// newest first").
func (m Model) load() tea.Cmd {
	src := m.events
	lister := m.lister
	namespace := m.namespace
	objectKind := m.objectKind
	objectName := m.objectName
	timeout := m.timeout

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		rawEvents, err := eventsForScope(ctx, src, lister, namespace, objectKind, objectName)
		if err != nil {
			return loadedMsg{err: err}
		}

		eventEntries := kube.TimelineFromEvents(kube.DedupeEvents(rawEvents))
		restartEntries := restartsForScope(ctx, lister, namespace, objectKind, objectName)
		rolloutEntries, rail, railDeployment := rolloutsForScope(ctx, lister, namespace, objectKind, objectName)
		attachChangeCause(ctx, lister, namespace, rolloutEntries)
		attachChangeCause(ctx, lister, namespace, rail)

		merged := kube.MergeTimeline(eventEntries, restartEntries, rolloutEntries)
		return loadedMsg{entries: merged, rail: rail, railDeployment: railDeployment}
	}
}

// eventsForScope fetches every event relevant to the screen's scope: the
// primary object's own events (ObjectEvents), plus — for scopes that own
// pods (Node, Deployment, StatefulSet, DaemonSet) — events on each of "its
// pods" too, the same promise feedHeader's own "WHAT — <kind>/<name> + its
// pods" text already makes. A container-level event like
// CreateContainerError/BackOff/Unhealthy is always emitted with
// involvedObject == the Pod, never the owning Deployment, so ObjectEvents on
// the Deployment alone silently drops it — exactly the gap this closes.
func eventsForScope(ctx context.Context, src EventsReader, lister resources.RawLister, namespace string, objectKind kube.ResourceKind, objectName string) ([]kube.Event, error) {
	if objectKind == "" {
		return src.NamespaceEvents(ctx, namespace)
	}
	primary, err := src.ObjectEvents(ctx, namespace, objectKind, objectName)
	if err != nil {
		return nil, err
	}
	podNames := podsOwnedByScope(ctx, lister, namespace, objectKind, objectName)
	if len(podNames) == 0 {
		return primary, nil
	}
	all, err := src.NamespaceEvents(ctx, namespace)
	if err != nil {
		// Best-effort: still show the primary object's own events rather
		// than failing the whole load over the owned-pods lookup.
		return primary, nil
	}
	out := append([]kube.Event(nil), primary...)
	for _, e := range all {
		kind, name := splitObject(e.Object)
		if kind == kube.KindPod && podNames[name] {
			out = append(out, e)
		}
	}
	return out, nil
}

// restartsForScope reads container-restart entries for the screen's scope:
// every pod in namespace (16a), the one pod (16b on a Pod), every pod
// scheduled on the node (16b on a Node), or every pod owned by the
// Deployment/StatefulSet/DaemonSet (16b on one of those, via
// podsOwnedByScope) — nil (best-effort, matching tasks/events' own
// failingPods degrade-gracefully precedent) when lister isn't wired, and
// skipped entirely for object kinds with no pod-ownership concept at all
// (a Service, say).
func restartsForScope(ctx context.Context, lister resources.RawLister, namespace string, objectKind kube.ResourceKind, objectName string) []kube.TimelineEntry {
	if lister == nil {
		return nil
	}
	objs, err := lister.ListRaw(ctx, kube.KindPod, namespace)
	if err != nil {
		return nil
	}
	var scopedNames map[string]bool
	switch objectKind {
	case kube.KindDeployment, kube.KindStatefulSet, kube.KindDaemonSet:
		scopedNames = podsOwnedByScope(ctx, lister, namespace, objectKind, objectName)
	}
	var pods []kube.Pod
	for _, obj := range objs {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		switch objectKind {
		case "":
			// 16a: every pod in scope counts.
		case kube.KindPod:
			if pod.Name != objectName {
				continue
			}
		case kube.KindNode:
			if pod.Spec.NodeName != objectName {
				continue
			}
		case kube.KindDeployment, kube.KindStatefulSet, kube.KindDaemonSet:
			if !scopedNames[pod.Name] {
				continue
			}
		default:
			continue
		}
		pods = append(pods, kube.PodFromObject(pod))
	}
	return kube.TimelineFromRestarts(pods)
}

// podsOwnedByScope resolves the pod names "owned" by objectKind/objectName
// for scopes where that concept applies: scheduled there (Node), or owned
// directly (StatefulSet/DaemonSet) or via an intermediate ReplicaSet
// (Deployment). nil for every other kind, including Pod itself — a Pod's
// own scope needs no separate resolution, it already is the one pod.
func podsOwnedByScope(ctx context.Context, lister resources.RawLister, namespace string, objectKind kube.ResourceKind, objectName string) map[string]bool {
	if lister == nil {
		return nil
	}
	switch objectKind {
	case kube.KindNode, kube.KindDeployment, kube.KindStatefulSet, kube.KindDaemonSet:
	default:
		return nil
	}

	podObjs, err := lister.ListRaw(ctx, kube.KindPod, namespace)
	if err != nil {
		return nil
	}
	var rsToDeployment map[string]string
	if objectKind == kube.KindDeployment {
		rsToDeployment = replicaSetOwners(ctx, lister, namespace)
	}

	names := make(map[string]bool)
	for _, obj := range podObjs {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		switch objectKind {
		case kube.KindNode:
			if pod.Spec.NodeName == objectName {
				names[pod.Name] = true
			}
		case kube.KindStatefulSet, kube.KindDaemonSet:
			if ownerKind, ownerName, ok := splitOwner(ownerRef(pod.OwnerReferences)); ok && ownerKind == objectKind && ownerName == objectName {
				names[pod.Name] = true
			}
		case kube.KindDeployment:
			if ownerKind, ownerName, ok := splitOwner(ownerRef(pod.OwnerReferences)); ok && ownerKind == kube.KindReplicaSet && rsToDeployment[ownerName] == objectName {
				names[pod.Name] = true
			}
		}
	}
	return names
}

// replicaSetOwners maps every ReplicaSet name in namespace to its owning
// Deployment name (when any) — built once so podsOwnedByScope's Deployment
// case doesn't re-list/re-walk ReplicaSets per pod.
func replicaSetOwners(ctx context.Context, lister resources.RawLister, namespace string) map[string]string {
	objs, err := lister.ListRaw(ctx, kube.KindReplicaSet, namespace)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(objs))
	for _, obj := range objs {
		rs, ok := obj.(*appsv1.ReplicaSet)
		if !ok {
			continue
		}
		if depKind, depName, ok := splitOwner(ownerRef(rs.OwnerReferences)); ok && depKind == kube.KindDeployment {
			out[rs.Name] = depName
		}
	}
	return out
}

// rolloutsForScope reads Deployment rollout-revision entries: every
// Deployment's revisions in namespace (16a, no rail — mixed objects don't
// get one), or the scope's resolved owning Deployment's revisions plus the
// 16b revision rail (newest-first, index 0 the current revision) when the
// object resolves to one. ("", nil, "") when it doesn't (a Node, or a Pod
// with no Deployment-owned ReplicaSet).
func rolloutsForScope(ctx context.Context, lister resources.RawLister, namespace string, objectKind kube.ResourceKind, objectName string) (entries, rail []kube.TimelineEntry, railDeployment string) {
	if lister == nil {
		return nil, nil, ""
	}
	objs, err := lister.ListRaw(ctx, kube.KindReplicaSet, namespace)
	if err != nil {
		return nil, nil, ""
	}
	all := kube.TimelineFromRollouts(objs)
	if objectKind == "" {
		return all, nil, ""
	}

	depName, ok := resolveOwningDeployment(ctx, lister, namespace, objectKind, objectName)
	if !ok {
		return nil, nil, ""
	}
	scoped := make([]kube.TimelineEntry, 0, len(all))
	for _, e := range all {
		if e.Object == "Deployment/"+depName {
			scoped = append(scoped, e)
		}
	}
	if len(scoped) == 0 {
		return nil, nil, ""
	}
	revisionRail := append([]kube.TimelineEntry(nil), scoped...)
	sort.Slice(revisionRail, func(i, j int) bool { return revisionRail[i].Revision > revisionRail[j].Revision })
	attachLiveRolloutStatus(ctx, lister, namespace, depName, revisionRail)
	return scoped, revisionRail, depName
}

// attachLiveRolloutStatus sets rail[0]'s LiveStatusText/LiveStatusBad from
// depName's own live Deployment object — the same resources.DeploymentRollout
// classification the browse table's ROLLOUT column uses — so the revision
// rail's current-revision card doesn't call a rollout "stable" while the
// Deployment itself is still progressing (new pods starting/being pulled) or
// degraded (stalled past its progress deadline). Best-effort, matching every
// other load.go helper: a nil lister, a list error, or no matching Deployment
// just leaves the fields at their zero value, falling back to the rail's
// restart-based stable/restarts-since text.
func attachLiveRolloutStatus(ctx context.Context, lister resources.RawLister, namespace, depName string, rail []kube.TimelineEntry) {
	if lister == nil || len(rail) == 0 {
		return
	}
	objs, err := lister.ListRaw(ctx, kube.KindDeployment, namespace)
	if err != nil {
		return
	}
	for _, obj := range objs {
		dep, ok := obj.(*appsv1.Deployment)
		if !ok || dep.Name != depName {
			continue
		}
		text, status := resources.DeploymentRollout(dep)
		if status == resources.StatusOK {
			return
		}
		rail[0].LiveStatusText = text
		rail[0].LiveStatusBad = status == resources.StatusFail
		return
	}
}

// changeCauseAnnotation is the standard `kubectl rollout history` /
// `--record` annotation key ("kubectl.kubernetes.io/change-cause") — 16a's
// optional "· by ci@github" attribution (docs/design README.md §16a) reads
// it straight off the Deployment; never fabricated when absent.
const changeCauseAnnotation = "kubectl.kubernetes.io/change-cause"

// attachChangeCause sets entries' By field from each entry's own Deployment
// object's change-cause annotation, in place — best-effort (nil lister or a
// list error just leaves every By empty, the same degrade-gracefully
// precedent restartsForScope/rolloutsForScope already follow).
func attachChangeCause(ctx context.Context, lister resources.RawLister, namespace string, entries []kube.TimelineEntry) {
	if lister == nil || len(entries) == 0 {
		return
	}
	objs, err := lister.ListRaw(ctx, kube.KindDeployment, namespace)
	if err != nil {
		return
	}
	causes := make(map[string]string, len(objs))
	for _, obj := range objs {
		dep, ok := obj.(*appsv1.Deployment)
		if !ok {
			continue
		}
		if cause := dep.Annotations[changeCauseAnnotation]; cause != "" {
			causes[dep.Namespace+"/"+dep.Name] = cause
		}
	}
	for i, e := range entries {
		kind, name := splitObject(e.Object)
		if kind != kube.KindDeployment {
			continue
		}
		entries[i].By = causes[e.Namespace+"/"+name]
	}
}

// resolveOwningDeployment resolves objectKind/objectName to an owning
// Deployment name — objectName itself when it already is one, or (for a
// Pod) a walk through its ReplicaSet owner to that ReplicaSet's own
// Deployment owner, the identical two-hop lookup poddetail's own
// resolveOwnerWorkload makes for its "alt+o" related-Deployment jump
// (duplicated per the repo's package-local-seam convention — task packages
// don't import each other). Every other object kind (Node, a StatefulSet-
// owned pod, …) has no rollout-revision concept, so ok is false.
func resolveOwningDeployment(ctx context.Context, lister resources.RawLister, namespace string, objectKind kube.ResourceKind, objectName string) (string, bool) {
	switch objectKind {
	case kube.KindDeployment:
		return objectName, true
	case kube.KindPod:
		objs, err := lister.ListRaw(ctx, kube.KindPod, namespace)
		if err != nil {
			return "", false
		}
		for _, obj := range objs {
			pod, ok := obj.(*corev1.Pod)
			if !ok || pod.Name != objectName {
				continue
			}
			ownerKind, ownerName, ok := splitOwner(ownerRef(pod.OwnerReferences))
			if !ok || ownerKind != kube.KindReplicaSet {
				return "", false
			}
			rsObjs, err := lister.ListRaw(ctx, kube.KindReplicaSet, namespace)
			if err != nil {
				return "", false
			}
			for _, rsObj := range rsObjs {
				rs, ok := rsObj.(*appsv1.ReplicaSet)
				if !ok || rs.Name != ownerName {
					continue
				}
				depKind, depName, ok := splitOwner(ownerRef(rs.OwnerReferences))
				if ok && depKind == kube.KindDeployment {
					return depName, true
				}
			}
			return "", false
		}
		return "", false
	default:
		return "", false
	}
}

// splitObject splits a kube.TimelineEntry.Object string ("Pod/nva-worker-
// 9k2ss") into its Kind and Name — mirrors tasks/events' own helper of the
// same name.
func splitObject(object string) (kube.ResourceKind, string) {
	kind, name, ok := strings.Cut(object, "/")
	if !ok {
		return "", ""
	}
	return kube.ResourceKind(kind), name
}

// splitOwner parses an "Owner/name" string (kube.PodFromObject's ownerRef
// shape) into its kind/name — mirrors poddetail's own helper of the same
// name.
func splitOwner(owner string) (kube.ResourceKind, string, bool) {
	kind, name, found := strings.Cut(owner, "/")
	if !found || kind == "" || name == "" {
		return "", "", false
	}
	return kube.ResourceKind(kind), name, true
}

// ownerRef mirrors kube.PodFromObject's own unexported helper of the same
// name — duplicated per the repo's small-pure-helper convention.
func ownerRef(refs []metav1.OwnerReference) string {
	if len(refs) == 0 {
		return ""
	}
	return refs[0].Kind + "/" + refs[0].Name
}
