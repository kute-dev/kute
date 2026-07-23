package kube

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TimelineEntryKind distinguishes 16a/16b's three merged-feed sources
// (docs/design README.md §16a: "events + container restarts + rollout
// revisions merged into a single feed").
type TimelineEntryKind int

const (
	TimelineEvent TimelineEntryKind = iota
	TimelineRestart
	TimelineRollout
)

// TimelineEntry is one row of 16a/16b's merged clock, newest-first once run
// through MergeTimeline.
type TimelineEntry struct {
	Time      time.Time
	Kind      TimelineEntryKind
	Object    string // "Kind/Name", e.g. "Pod/nva-worker-9k2ss"
	Namespace string
	Severity  string // "Warning" | "Normal" — TimelineEvent only
	Reason    string
	Message   string
	// Revision/Image are set on TimelineRollout entries only — the 16b
	// revision rail reads them directly rather than re-parsing Message.
	Revision int
	Image    string
	// By is a TimelineRollout entry's optional attribution — the owning
	// Deployment's own "kubectl.kubernetes.io/change-cause" annotation,
	// when present (16a's "· by ci@github", docs/design README.md §16a).
	// Left empty otherwise; never fabricated. Populated by
	// tasks/timeline's own load.go, not here — TimelineFromRollouts only
	// sees ReplicaSets, not their owning Deployment.
	By string
}

// TimelineFromEvents projects deduped EventGroups (DedupeEvents) into
// TimelineEntry rows.
func TimelineFromEvents(groups []EventGroup) []TimelineEntry {
	out := make([]TimelineEntry, 0, len(groups))
	for _, g := range groups {
		out = append(out, TimelineEntry{
			Time:      g.LastSeen,
			Kind:      TimelineEvent,
			Object:    g.Object,
			Namespace: g.Namespace,
			Severity:  g.Type,
			Reason:    g.Reason,
			Message:   g.Message,
		})
	}
	return out
}

// TimelineFromRestarts turns each pod's most recent container termination
// into one restart entry. The k8s API only retains the latest terminated
// state per container (ContainerStatus.LastTerminationState), so a pod's
// full restart history isn't reconstructable from the live cluster alone —
// one entry per pod's LastTermination is the same approximation 5a's own
// last-termination banner already makes.
func TimelineFromRestarts(pods []Pod) []TimelineEntry {
	out := make([]TimelineEntry, 0, len(pods))
	for _, p := range pods {
		lt := p.LastTermination
		if lt == nil || lt.FinishedAt.IsZero() {
			continue
		}
		out = append(out, TimelineEntry{
			Time:      lt.FinishedAt,
			Kind:      TimelineRestart,
			Object:    "Pod/" + p.Name,
			Namespace: p.Namespace,
			Reason:    "Restarted",
			Message:   fmt.Sprintf("%s · %s · exit %d", lt.Container, lt.Reason, lt.ExitCode),
		})
	}
	return out
}

// TimelineFromRollouts derives one entry per Deployment revision from its
// owned ReplicaSets' "deployment.kubernetes.io/revision" annotation — the
// same signal `kubectl rollout history` reads. objs is a ReplicaSet list
// (KindReplicaSet); callers may pre-filter it to one Deployment's owned set
// for 16b's object-scoped feed.
func TimelineFromRollouts(objs []runtime.Object) []TimelineEntry {
	out := make([]TimelineEntry, 0, len(objs))
	for _, obj := range objs {
		rs, ok := obj.(*appsv1.ReplicaSet)
		if !ok || len(rs.OwnerReferences) == 0 || rs.OwnerReferences[0].Kind != "Deployment" {
			continue
		}
		revText, ok := rs.Annotations["deployment.kubernetes.io/revision"]
		if !ok {
			continue
		}
		rev, _ := strconv.Atoi(revText)
		out = append(out, TimelineEntry{
			Time:      rs.CreationTimestamp.Time,
			Kind:      TimelineRollout,
			Object:    "Deployment/" + rs.OwnerReferences[0].Name,
			Namespace: rs.Namespace,
			Reason:    "Rollout",
			Message:   fmt.Sprintf("revision %d · %s", rev, rolloutReplicaSetImage(rs)),
			Revision:  rev,
			Image:     rolloutReplicaSetImage(rs),
		})
	}
	return out
}

func rolloutReplicaSetImage(rs *appsv1.ReplicaSet) string {
	containers := rs.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return "–"
	}
	img := containers[0].Image
	if len(containers) > 1 {
		img += fmt.Sprintf(" +%d", len(containers)-1)
	}
	return img
}

// MergeTimeline concatenates every source's entries and sorts them
// newest-first — 16a/16b's "one clock" (docs/design README.md §16a).
func MergeTimeline(sources ...[]TimelineEntry) []TimelineEntry {
	var out []TimelineEntry
	for _, s := range sources {
		out = append(out, s...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}
