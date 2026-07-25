package browse

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

// auxKinds maps a listed kind to the other kinds its rows and prompts read.
// A list is rarely built from one cache: the Deployments IMAGE column
// compares against ReplicaSets, the Ingresses BACKENDS column resolves
// through Services and Pods, the Pods health strip counts Nodes, and the
// scale prompt looks for a managing HorizontalPodAutoscaler.
//
// Two things use this. Reloading: a change to any of these kinds changes
// what the current list should show, and browse subscribed only to the kind
// it was displaying — so a Deployment's image cell went stale on a rollout
// until something else prompted a reload. Prefetching: informers start on
// first read, and several of these are read synchronously from a keypress
// handler with nowhere to put a loading state, so the first press would
// otherwise get an empty cache and quietly render a wrong-but-plausible
// answer ("no rollout history", "no HPA manages this").
var auxKinds = map[kube.ResourceKind][]kube.ResourceKind{
	kube.KindPod:  {kube.KindNode},
	kube.KindNode: {kube.KindPod},
	kube.KindDeployment: {
		kube.KindReplicaSet,              // IMAGE column + rollout history
		kube.KindHorizontalPodAutoscaler, // scale prompt
		kube.KindPod,                     // set-resources usage
	},
	kube.KindStatefulSet: {
		kube.KindControllerRevision, // rollout history
		kube.KindHorizontalPodAutoscaler,
		kube.KindPod,
	},
	kube.KindDaemonSet: {
		kube.KindControllerRevision,
		kube.KindPod,
	},
	kube.KindIngress:     {kube.KindService, kube.KindPod}, // BACKENDS column
	kube.KindHelmRelease: {kube.KindSecret},                // releases decode from Secrets
	kube.KindService:     {kube.KindPod},                   // label-join in the meta editor
}

// auxKindOf reports whether changed is one of listed's secondary kinds.
func auxKindOf(listed, changed kube.ResourceKind) bool {
	for _, k := range auxKinds[listed] {
		if k == changed {
			return true
		}
	}
	return false
}

// prefetchAuxKinds warms the caches the current kind's rows and prompts read,
// off the Update loop. Each read is what starts the kind's informer, so by
// the time the user has moved to a row and pressed a key, the data those
// synchronous handlers need has had seconds to arrive.
//
// Results are discarded: this is a cache-warming side effect, not a fetch.
// That keeps it free of any new interface — it works through the same
// RawLister every screen already holds, decorators included.
func (m Model) prefetchAuxKinds() tea.Cmd {
	kinds := auxKinds[m.kind]
	if len(kinds) == 0 || m.lister == nil {
		return nil
	}
	lister, namespace := m.lister, m.namespace
	return func() tea.Msg {
		for _, kind := range kinds {
			//nolint:errcheck // warming a cache; a failure just means the read happens later
			_, _ = lister.ListRaw(context.Background(), kind, namespace)
		}
		return nil
	}
}
