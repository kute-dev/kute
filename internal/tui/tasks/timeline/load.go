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

		var rawEvents []kube.Event
		var err error
		if objectKind != "" {
			rawEvents, err = src.ObjectEvents(ctx, namespace, objectKind, objectName)
		} else {
			rawEvents, err = src.NamespaceEvents(ctx, namespace)
		}
		if err != nil {
			return loadedMsg{err: err}
		}

		eventEntries := kube.TimelineFromEvents(kube.DedupeEvents(rawEvents))
		restartEntries := restartsForScope(ctx, lister, namespace, objectKind, objectName)
		rolloutEntries, rail, railDeployment := rolloutsForScope(ctx, lister, namespace, objectKind, objectName)

		merged := kube.MergeTimeline(eventEntries, restartEntries, rolloutEntries)
		return loadedMsg{entries: merged, rail: rail, railDeployment: railDeployment}
	}
}

// restartsForScope reads container-restart entries for the screen's scope:
// every pod in namespace (16a), the one pod (16b on a Pod), or every pod
// scheduled on the node (16b on a Node) — nil (best-effort, matching
// tasks/events' own failingPods degrade-gracefully precedent) when lister
// isn't wired, and skipped entirely for object kinds restarts aren't
// meaningful for (a Deployment, say — its pods' restarts still surface via
// each pod's own namespace-scoped entry).
func restartsForScope(ctx context.Context, lister resources.RawLister, namespace string, objectKind kube.ResourceKind, objectName string) []kube.TimelineEntry {
	if lister == nil {
		return nil
	}
	objs, err := lister.ListRaw(ctx, kube.KindPod, namespace)
	if err != nil {
		return nil
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
		default:
			continue
		}
		pods = append(pods, kube.PodFromObject(pod))
	}
	return kube.TimelineFromRestarts(pods)
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
	return scoped, revisionRail, depName
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
