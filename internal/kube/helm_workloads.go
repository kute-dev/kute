package kube

import (
	"iter"
	"slices"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

// WorkloadRef names one workload object by kind, namespace and name — the
// identity a cache lookup needs, with no object attached.
type WorkloadRef struct {
	Kind      ResourceKind
	Namespace string
	Name      string
}

// helmWorkloadKinds are the kinds HelmReleaseWorkloads reports: the three
// that actually roll. A release also owns Services, ConfigMaps, RBAC and the
// rest, but none of those have a rollout to be mid-way through, so reading
// them would cost caches to learn nothing.
var helmWorkloadKinds = map[string]ResourceKind{
	"Deployment":  KindDeployment,
	"StatefulSet": KindStatefulSet,
	"DaemonSet":   KindDaemonSet,
}

// HelmReleaseWorkloads lists the workloads a release's own rendered manifest
// declares — the release→workload link 18a's rollout signal needs.
//
// The manifest is the authority here, not labels. Helm itself stamps nothing
// onto the objects it applies; `app.kubernetes.io/instance` is a *chart*
// convention (helm create's default templates), so a label join silently
// finds nothing for any chart that doesn't follow it. The manifest is already
// decoded into HelmRelease.Manifest, and it is exactly the set of objects
// this revision applied.
//
// Parsing is deliberately two-stage: a cheap scan for a top-level `kind:`
// line, then a real YAML unmarshal only for the documents that turned out to
// be workloads. A big chart's manifest runs to hundreds of KB of Services,
// ConfigMaps and RBAC, and this runs on every read of the Helm list — a full
// parse of all of it, per release, per reload, is not affordable. Documents
// that don't parse are skipped rather than failing the list: an unreadable
// manifest costs the signal, never the screen.
func HelmReleaseWorkloads(r HelmRelease) []WorkloadRef {
	return slices.Collect(HelmReleaseWorkloadsSeq(r))
}

// HelmReleaseWorkloadsSeq is the iterator behind HelmReleaseWorkloads, for the
// callers that only want to know whether *some* workload matches.
//
// app.go's rolloutPending is the one that matters: it ranges this once and
// returns on the first hit, so as a sequence the unmarshal of every later
// workload document never happens. Splitting is lazy for the same reason —
// the whole manifest no longer becomes a []string up front.
func HelmReleaseWorkloadsSeq(r HelmRelease) iter.Seq[WorkloadRef] {
	return func(yield func(WorkloadRef) bool) {
		for doc := range splitYAMLDocuments(r.Manifest) {
			kind, ok := helmWorkloadKinds[topLevelKind(doc)]
			if !ok {
				continue
			}
			var meta struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
			}
			if err := sigsyaml.Unmarshal([]byte(doc), &meta); err != nil || meta.Metadata.Name == "" {
				continue
			}
			namespace := meta.Metadata.Namespace
			if namespace == "" {
				// A chart that doesn't template metadata.namespace lands in
				// the release's namespace, which is what `helm install -n`
				// means.
				namespace = r.Namespace
			}
			if !yield(WorkloadRef{Kind: kind, Namespace: namespace, Name: meta.Metadata.Name}) {
				return
			}
		}
	}
}

// splitYAMLDocuments splits a multi-document manifest on its `---`
// separators. Only a separator at column 0 counts, so an indented `---`
// inside a block scalar doesn't split a document in half.
func splitYAMLDocuments(manifest string) iter.Seq[string] {
	return func(yield func(string) bool) {
		var cur strings.Builder
		for line := range strings.SplitSeq(manifest, "\n") {
			if strings.TrimRight(line, " \t\r") == "---" {
				if !yield(cur.String()) {
					return
				}
				cur.Reset()
				continue
			}
			cur.WriteString(line)
			cur.WriteString("\n")
		}
		yield(cur.String())
	}
}

// topLevelKind reads a document's `kind:` without parsing it. Only an
// unindented match counts — a nested `kind:` (an ownerReference, a
// PodTemplate's own fields, a CRD's schema) is not the document's own kind.
func topLevelKind(doc string) string {
	for _, line := range strings.Split(doc, "\n") {
		rest, ok := strings.CutPrefix(line, "kind:")
		if !ok {
			continue
		}
		return strings.Trim(strings.TrimSpace(rest), `"'`)
	}
	return ""
}
