package kube

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
	// TimelineRevision is §32a's git row: a Flux reconciler applied a new
	// source revision. Its own kind rather than a TimelineEvent because it
	// answers the timeline's actual question ("what changed?") on a GitOps
	// cluster, and gets its own marker to say so.
	TimelineRevision
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
	// GitRevision/CommitSubject are set on TimelineRevision entries only.
	// GitRevision is the shortened "master@efd398b"; CommitSubject is the
	// commit's first line, which source-controller publishes *only* in an
	// event message and which therefore may legitimately be empty — the
	// artifact carries no commit metadata and kute reads no git remote
	// (docs/design README.md §31a). Never fabricated.
	GitRevision   string
	CommitSubject string
	// By is a TimelineRollout entry's optional attribution — the owning
	// Deployment's own "kubectl.kubernetes.io/change-cause" annotation,
	// when present (16a's "· by ci@github", docs/design README.md §16a).
	// Left empty otherwise; never fabricated. Populated by
	// tasks/timeline's own load.go, not here — TimelineFromRollouts only
	// sees ReplicaSets, not their owning Deployment.
	By string
	// LiveStatusText/LiveStatusBad are the 16b revision rail's
	// current-revision (index 0 only) live rollout-progress override — set
	// when the owning Deployment's own resources.DeploymentRollout status
	// isn't "stable" (still progressing, or degraded/stalled), so the rail
	// doesn't call a rollout "stable" while new pods are still starting.
	// Empty/false means "use the restart-based stable/restarts-since text
	// instead." Populated by tasks/timeline's own load.go, not here (same
	// reasoning as By); never set on any entry but rail[0].
	LiveStatusText string
	LiveStatusBad  bool
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

// fluxRevisionAnnotations are the per-controller annotations Flux stamps on
// its own reconcile Events, carrying the revision it just applied. Reading
// the annotation rather than parsing the message is the whole point: the
// message is prose that has changed between Flux versions, the annotation
// is data.
var fluxRevisionAnnotations = []string{
	"kustomize.toolkit.fluxcd.io/revision",
	"helm.toolkit.fluxcd.io/revision",
	"source.toolkit.fluxcd.io/revision",
}

// storedArtifactCommit matches source-controller's own "stored artifact"
// event message, the one place on the cluster a commit subject appears.
// Anchored on the message shape rather than the event Reason, which has
// varied between NewArtifact and GitOperationSucceeded across versions.
var storedArtifactCommit = regexp.MustCompile(`stored artifact for commit '(.+)'`)

// FluxCommitSubject extracts the commit subject from a source-controller
// event message, or "" when the message isn't that one.
func FluxCommitSubject(message string) string {
	m := storedArtifactCommit.FindStringSubmatch(message)
	if len(m) != 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// TimelineFromFluxEvents projects Flux reconcile events carrying a revision
// annotation into §32a's revision rows, newest-first once merged.
//
// subjects maps a revision to its commit subject, gathered from
// source-controller's own events; a revision missing from it renders as the
// bare SHA, which is the honest degradation — Events expire, and there is
// no other on-cluster source for the subject.
func TimelineFromFluxEvents(events []Event, subjects map[string]string) []TimelineEntry {
	var out []TimelineEntry
	for _, e := range events {
		rev := FluxEventRevision(e)
		if rev == "" {
			continue
		}
		out = append(out, TimelineEntry{
			Time:          e.LastSeen,
			Kind:          TimelineRevision,
			Object:        e.Object,
			Namespace:     e.Namespace,
			Reason:        e.Reason,
			GitRevision:   ShortFluxRevision(rev),
			CommitSubject: subjects[rev],
		})
	}
	return out
}

// FluxCommitSubjects builds the revision→subject index for one namespace's
// events: every "stored artifact for commit" message paired with the
// revision its own source object reports.
func FluxCommitSubjects(events []Event, revisionOf func(object string) string) map[string]string {
	out := map[string]string{}
	for _, e := range events {
		subject := FluxCommitSubject(e.Message)
		if subject == "" {
			continue
		}
		if rev := revisionOf(e.Object); rev != "" {
			out[rev] = subject
		}
	}
	return out
}

// FluxEventRevision reads whichever Flux revision annotation the event
// carries.
func FluxEventRevision(e Event) string {
	for _, key := range fluxRevisionAnnotations {
		if v := e.Annotations[key]; v != "" {
			return v
		}
	}
	return ""
}

// ShortFluxRevision collapses "master@sha1:efd398be…" to "master@efd398b",
// and a bare "sha256:d83a…" digest to its first 7 hex characters. A plain
// version string is returned unchanged.
func ShortFluxRevision(rev string) string {
	ref := ""
	digest := rev
	if at := strings.LastIndex(rev, "@"); at >= 0 {
		ref, digest = rev[:at], rev[at+1:]
	}
	colon := strings.Index(digest, ":")
	if colon < 0 {
		return rev
	}
	digest = digest[colon+1:]
	if len(digest) > 7 {
		digest = digest[:7]
	}
	switch {
	case digest == "":
		return ref
	case ref == "":
		return digest
	}
	return ref + "@" + digest
}
