package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/kube/fake"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/tasks/browse"
	"github.com/kute-dev/kute/internal/tui/tasks/configmapdata"
	"github.com/kute-dev/kute/internal/tui/tasks/events"
	"github.com/kute-dev/kute/internal/tui/tasks/execpicker"
	"github.com/kute-dev/kute/internal/tui/tasks/forwardpicker"
	"github.com/kute-dev/kute/internal/tui/tasks/helmhistory"
	"github.com/kute-dev/kute/internal/tui/tasks/nodedetail"
	"github.com/kute-dev/kute/internal/tui/tasks/objectdetail"
	"github.com/kute-dev/kute/internal/tui/tasks/overview"
	"github.com/kute-dev/kute/internal/tui/tasks/poddetail"
	"github.com/kute-dev/kute/internal/tui/tasks/podlogs"
	"github.com/kute-dev/kute/internal/tui/tasks/routetable"
	"github.com/kute-dev/kute/internal/tui/tasks/secretdata"
	"github.com/kute-dev/kute/internal/tui/tasks/setup"
	"github.com/kute-dev/kute/internal/tui/tasks/timeline"
	"github.com/kute-dev/kute/internal/tui/tasks/whocan"
	"github.com/kute-dev/kute/internal/tui/tasks/yamlview"
)

// forwardAwareLister dispatches kube.KindForward to the shared
// *kube.ForwardManager and everything else to the underlying cluster
// lister — the seam that lets Forwards flow through browse's normal
// resources.List/goto-palette pipeline as "just another kind" (docs/design
// README.md §13c) without kube.Cluster/fake.Cluster needing to know
// anything about forwarding.
type forwardAwareLister struct {
	resources.RawLister
	forwards *kube.ForwardManager
}

func (l forwardAwareLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	if kind == kube.KindForward {
		return l.forwards.ListRaw(), nil
	}
	return l.RawLister.ListRaw(ctx, kind, namespace)
}

// helmAwareLister decodes kube.KindHelmRelease from the already-watched
// Secret cache (docs/design README.md §18a: "browsing needs no helm
// binary") — the same "a kind computed from another kind's own cache, not
// its own informer" shape forwardAwareLister already establishes for
// KindForward, just reading Secrets instead of maintaining its own state.
type helmAwareLister struct {
	resources.RawLister
}

func (l helmAwareLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	if kind != kube.KindHelmRelease {
		return l.RawLister.ListRaw(ctx, kind, namespace)
	}
	secrets, err := l.RawLister.ListRaw(ctx, kube.KindSecret, namespace)
	if err != nil {
		return nil, err
	}
	releases := kube.LatestHelmReleases(kube.DecodeHelmReleases(secrets))
	out := make([]runtime.Object, len(releases))
	for i, r := range releases {
		out[i] = kube.NewHelmReleaseObject(r)
	}
	return out, nil
}

// Synced forwards to the wrapped lister's own Synced — same reasoning as
// forwardAwareLister.Synced (embedding an interface field only promotes the
// interface's own methods).
func (l helmAwareLister) Synced() bool {
	if sc, ok := l.RawLister.(cacheSyncChecker); ok {
		return sc.Synced()
	}
	return true
}

// cacheSyncChecker mirrors browse.CacheSyncChecker structurally so this
// package doesn't need to import browse just for the type assertion below.
type cacheSyncChecker interface{ Synced() bool }

// Synced forwards to the wrapped lister's own Synced, if it has one.
// Embedding resources.RawLister as an interface field only promotes methods
// that interface declares (ListRaw), so *kube.Cluster's Synced silently
// stopped being visible through forwardAwareLister without this — browse's
// listerSynced type-assertion against the wrapper (not the cluster) always
// missed, so a load reply landing before the informer cache's initial sync
// read as a genuinely empty result (10c) instead of "still loading" (15a).
// *fake.Cluster doesn't implement Synced either, so this still falls
// through to "synced" for --demo, same as before.
func (l forwardAwareLister) Synced() bool {
	if sc, ok := l.RawLister.(cacheSyncChecker); ok {
		return sc.Synced()
	}
	return true
}

// Compile-time guarantees that both the informer-backed Cluster and the
// in-memory fake satisfy the seams the screens depend on (--demo
// substitutes the latter behind the same interfaces, mvp-plan.md §0.10).
var (
	_ resources.RawLister        = (*kube.Cluster)(nil)
	_ kube.Mutator               = (*kube.Cluster)(nil)
	_ browse.MetricsReader       = (*kube.Cluster)(nil)
	_ browse.NodeMetricsReader   = (*kube.Cluster)(nil)
	_ poddetail.EventsReader     = (*kube.Cluster)(nil)
	_ yamlview.YAMLReader        = (*kube.Cluster)(nil)
	_ events.EventsReader        = (*kube.Cluster)(nil)
	_ objectdetail.EventsReader  = (*kube.Cluster)(nil)
	_ timeline.EventsReader      = (*kube.Cluster)(nil)
	_ resources.InstanceCounter  = (*kube.Cluster)(nil)
	_ whocan.WhoCanReader        = (*kube.Cluster)(nil)
	_ overview.NodeMetricsReader = (*kube.Cluster)(nil)
	_ resources.RawLister        = (*fake.Cluster)(nil)
	_ kube.Mutator               = (*fake.Cluster)(nil)
	_ browse.MetricsReader       = (*fake.Cluster)(nil)
	_ browse.NodeMetricsReader   = (*fake.Cluster)(nil)
	_ poddetail.EventsReader     = (*fake.Cluster)(nil)
	_ yamlview.YAMLReader        = (*fake.Cluster)(nil)
	_ events.EventsReader        = (*fake.Cluster)(nil)
	_ objectdetail.EventsReader  = (*fake.Cluster)(nil)
	_ timeline.EventsReader      = (*fake.Cluster)(nil)
	_ resources.InstanceCounter  = (*fake.Cluster)(nil)
	_ whocan.WhoCanReader        = (*fake.Cluster)(nil)
	_ overview.NodeMetricsReader = (*fake.Cluster)(nil)
)

// seams is what browse, nodedetail, poddetail, yamlview, events, and
// objectdetail need from either a real or fake cluster, satisfied directly
// by both *kube.Cluster and *fake.Cluster.
type seams interface {
	resources.RawLister
	browse.MetricsReader
	browse.NodeMetricsReader
	kube.Mutator
	poddetail.EventsReader
	yamlview.YAMLReader
	events.EventsReader
	objectdetail.EventsReader
	timeline.EventsReader
	whocan.WhoCanReader
}

// NewModel builds the root model. It's rooted at tasks/browse when a
// cluster (real or --demo) is available, or tasks/setup's 10b "no
// kubeconfig" state otherwise (mvp-plan.md Phase 4) — a real cluster that
// exists but hasn't yet answered (4c) is detected later, once the program
// is running, by the root shell's neverConnected watch (see
// buildSetupFactory/tui.Model.WithRootFactories). It returns the real
// Cluster (non-nil unless cfg.Demo or the cluster is unreachable) and the
// demo Cluster (non-nil only in demo mode) so Run can drive whichever one
// is active.
func NewModel(cfg Config) (tui.Model, *kube.Cluster, *fake.Cluster) {
	sess, cluster, buildErr := BuildSession(cfg)
	checker := buildChecker(cfg)

	switch {
	case cluster != nil:
		sess.Forwards = kube.NewForwardManager()
		sess.Lister = helmAwareLister{RawLister: forwardAwareLister{RawLister: cluster, forwards: sess.Forwards}}
		sess.Metrics = cluster
		model := tui.NewWithSession(buildBrowseTask(cfg, sess, cluster), sess).
			WithRootFactories(buildSetupFactory(cfg, sess, cluster), buildBrowseFactory(cfg, sess, cluster)).
			WithUpdatePanel(buildUpdateFactory(sess, checker))
		return model, cluster, nil

	case cfg.Demo:
		demoCluster := fake.NewDemo()
		clusterName, namespace := demoCluster.CurrentContext(), demoCluster.CurrentNamespace()
		sess.Location.Context = clusterName
		sess.Location.Namespace = namespace
		sess.Registry, sess.Groups = resources.BuildDiscoveredRegistry(demoCluster.DiscoveredKinds(), demoCluster)
		sess.Forwards = kube.NewForwardManager()
		lister := helmAwareLister{RawLister: forwardAwareLister{RawLister: demoCluster, forwards: sess.Forwards}}
		sess.Lister = lister
		sess.Metrics = demoCluster
		openLogs := openLogsFunc(sess, demoCluster, demoCluster, clusterName, namespace)
		openYAML := openYAMLFunc(sess, demoCluster)
		openExec := openExecFunc(sess)
		openForward := openForwardFuncDemo(sess, lister, sess.Forwards, demoCluster)
		openPodDetail := openPodDetailFunc(sess, demoCluster, openLogs, openYAML, openExec, openForward)
		openNodeDetail := openNodeDetailFunc(sess, demoCluster, openPodDetail, openLogs, openYAML, openExec, openForward)
		openEvents := openEventsFunc(sess, demoCluster, openYAML)
		openTimeline := openTimelineFunc(sess, demoCluster, openEvents)
		b := browse.New(browse.Config{
			Session:           sess,
			Lister:            lister,
			Metrics:           demoCluster,
			NodeMetrics:       demoCluster,
			Mutator:           demoCluster,
			OpenLogs:          openLogs,
			OpenNodeDetail:    openNodeDetail,
			OpenPodDetail:     openPodDetail,
			OpenYAML:          openYAML,
			OpenEvents:        openEvents,
			OpenTimeline:      openTimeline,
			OpenExec:          browse.OpenExecFunc(openExec),
			OpenForward:       openForward,
			OpenObjectDetail:  openObjectDetailFunc(sess, demoCluster, openYAML),
			OpenRouteTable:    openRouteTableFunc(sess, demoCluster, openYAML),
			OpenWhoCan:        openWhoCanFunc(sess, demoCluster),
			OpenHelmHistory:   openHelmHistoryFunc(sess, demoCluster),
			OpenHelmValues:    openHelmValuesFunc(sess),
			OpenSecretData:    openSecretDataFunc(sess, demoCluster),
			OpenConfigMapData: openConfigMapDataFunc(sess, demoCluster),
			OpenOverview:      openOverviewFunc(sess, lister, demoCluster, openNodeDetail, openTimeline, openEvents),
			Forwards:          sess.Forwards,
			Retrier:           demoCluster,
		})
		model := tui.NewWithSession(&b, sess).WithUpdatePanel(buildUpdateFactory(sess, checker))
		return model, nil, demoCluster

	default:
		// No reachable cluster and not --demo: root at tasks/setup's 10b
		// "no kubeconfig" state, letting the user fix $KUBECONFIG/point at
		// a different path and retry in place.
		sess.Location.Context = cfg.Cluster
		s := setup.New(setup.Config{
			Session:        sess,
			State:          setup.NoConfig,
			Err:            buildErr,
			KubeconfigPath: kubeconfigPathOrEmpty(),
			Reconnect:      func(path string) tea.Cmd { return attemptReconnect(cfg, sess, path) },
		})
		model := tui.NewWithSession(&s, sess).WithUpdatePanel(buildUpdateFactory(sess, checker))
		return model, nil, nil
	}
}

// buildBrowseTask constructs browse against cluster, per repo convention —
// shared by NewModel's initial wiring and attemptReconnect's post-retry
// rebuild so the two never drift.
func buildBrowseTask(cfg Config, sess *tui.Session, cluster *kube.Cluster) *browse.Model {
	streamer := liveClusterLogStreamer{cluster: cluster}
	clusterName, namespace := cluster.Context.ClusterName, cluster.Context.Namespace
	lister := helmAwareLister{RawLister: forwardAwareLister{RawLister: cluster, forwards: sess.Forwards}}
	openLogs := openLogsFunc(sess, cluster, streamer, clusterName, namespace)
	openYAML := openYAMLFunc(sess, cluster)
	openExec := openExecFunc(sess)
	openForward := openForwardFunc(sess, lister, cluster)
	openPodDetail := openPodDetailFunc(sess, cluster, openLogs, openYAML, openExec, openForward)
	openNodeDetail := openNodeDetailFunc(sess, cluster, openPodDetail, openLogs, openYAML, openExec, openForward)
	openEvents := openEventsFunc(sess, cluster, openYAML)
	openTimeline := openTimelineFunc(sess, cluster, openEvents)
	b := browse.New(browse.Config{
		Session:           sess,
		Lister:            lister,
		Metrics:           cluster,
		NodeMetrics:       cluster,
		Mutator:           cluster,
		OpenLogs:          openLogs,
		OpenNodeDetail:    openNodeDetail,
		OpenPodDetail:     openPodDetail,
		OpenYAML:          openYAML,
		OpenEvents:        openEvents,
		OpenTimeline:      openTimeline,
		OpenExec:          browse.OpenExecFunc(openExec),
		OpenForward:       openForward,
		OpenObjectDetail:  openObjectDetailFunc(sess, cluster, openYAML),
		OpenRouteTable:    openRouteTableFunc(sess, cluster, openYAML),
		OpenWhoCan:        openWhoCanFunc(sess, cluster),
		OpenHelmHistory:   openHelmHistoryFunc(sess, cluster),
		OpenHelmValues:    openHelmValuesFunc(sess),
		OpenSecretData:    openSecretDataFunc(sess, cluster),
		OpenConfigMapData: openConfigMapDataFunc(sess, cluster),
		OpenOverview:      openOverviewFunc(sess, lister, cluster, openNodeDetail, openTimeline, openEvents),
		Forwards:          sess.Forwards,
		Retrier:           cluster,
	})
	return &b
}

// buildSetupFactory returns the closure tui.Model calls to build 4c's
// unreachable-at-launch screen for cluster — installed via
// WithRootFactories/ReplaceRootMsg since tui itself can't import
// tasks/setup (import cycle: setup, like every task, imports tui).
func buildSetupFactory(cfg Config, sess *tui.Session, cluster *kube.Cluster) func(kube.ConnState) tui.Task {
	return func(conn kube.ConnState) tui.Task {
		s := setup.New(setup.Config{
			Session:         sess,
			State:           setup.Unreachable,
			ClusterName:     cluster.Context.ContextName,
			Conn:            conn,
			OtherContexts:   otherContexts(cluster.Context.ContextName),
			KubeconfigPath:  kubeconfigPathOrEmpty(),
			RetryNow:        cluster.RetryNow,
			Reconnect:       func(path string) tea.Cmd { return attemptReconnect(cfg, sess, path) },
			SwitchToContext: func(name string) tea.Cmd { return attemptSwitchContext(cfg, sess, name) },
		})
		return &s
	}
}

// buildBrowseFactory returns the closure tui.Model calls to swap back to
// browse once a never-yet-connected cluster reports its first Connected
// state — same import-cycle reasoning as buildSetupFactory.
func buildBrowseFactory(cfg Config, sess *tui.Session, cluster *kube.Cluster) func() tui.Task {
	return func() tui.Task { return buildBrowseTask(cfg, sess, cluster) }
}

// attemptReconnect is tasks/setup's Reconnect hook (10b's 'r'/'k', 4c's
// 'e'): optionally repoints $KUBECONFIG, stops any existing cluster (so its
// informer goroutines don't leak), and builds a fresh one. On success it
// starts that cluster's informers (bounded, like 7a's SwitchContext) and
// hands back a tui.ReplaceRootMsg carrying a fresh browse task plus the new
// cluster's event channels — the root shell forwards those via
// tui.WatchCluster without needing a *tea.Program reference, since this Cmd
// runs long after RunWithConfig's own forwardEvents goroutine (bound to
// whatever cluster existed at process start) was already launched.
func attemptReconnect(cfg Config, sess *tui.Session, path string) tea.Cmd {
	return func() tea.Msg {
		if path != "" {
			_ = os.Setenv("KUBECONFIG", path)
		}
		if sess.Cluster != nil {
			sess.Cluster.Stop()
		}
		cluster, err := kube.NewCluster()
		if err != nil {
			return setup.RetryFailedMsg{Err: err}
		}

		ctx, cancel := context.WithTimeout(context.Background(), reconnectStartTimeout)
		defer cancel()
		_ = cluster.Start(ctx) // best-effort: an unreachable cluster still hands back ReplaceRootMsg; 4c's neverConnected watch takes over from there.

		if sess.Forwards == nil {
			sess.Forwards = kube.NewForwardManager()
		}
		sess.Cluster = cluster
		sess.Lister = helmAwareLister{RawLister: forwardAwareLister{RawLister: cluster, forwards: sess.Forwards}}
		sess.Metrics = cluster
		sess.Location.Context = cluster.Context.ContextName
		sess.Location.Namespace = cluster.Context.Namespace
		// cluster.Start above already ran discovery synchronously (folded
		// in, best-effort), so DiscoveredKinds is ready by now — no need
		// for the async CRDsDiscoveredMsg hop this path's initial-connect
		// counterpart (RunWithConfig) requires.
		sess.Registry, sess.Groups = resources.BuildDiscoveredRegistry(cluster.DiscoveredKinds(), cluster)

		return tui.ReplaceRootMsg{
			Task:        buildBrowseTask(cfg, sess, cluster),
			Events:      cluster.Events(),
			Conn:        cluster.ConnEvents(),
			BuildSetup:  buildSetupFactory(cfg, sess, cluster),
			BuildBrowse: buildBrowseFactory(cfg, sess, cluster),
		}
	}
}

// attemptSwitchContext is 4c's SWITCH CONTEXT list's '↵' (docs/design
// README.md §4c: "↵ connect to selected") — the same shape as
// attemptReconnect, but pinned to a named kubeconfig context via
// kube.NewClusterForContext instead of re-resolving $KUBECONFIG's own
// current-context, so selecting a reachable sibling context actually
// switches to it rather than retrying the same failing one.
func attemptSwitchContext(cfg Config, sess *tui.Session, contextName string) tea.Cmd {
	return func() tea.Msg {
		if sess.Cluster != nil {
			sess.Cluster.Stop()
		}
		cluster, err := kube.NewClusterForContext(contextName)
		if err != nil {
			return setup.RetryFailedMsg{Err: err}
		}

		ctx, cancel := context.WithTimeout(context.Background(), reconnectStartTimeout)
		defer cancel()
		_ = cluster.Start(ctx) // best-effort: an unreachable target still hands back ReplaceRootMsg; 4c's neverConnected watch takes over from there.

		if sess.Forwards == nil {
			sess.Forwards = kube.NewForwardManager()
		}
		sess.Cluster = cluster
		sess.Lister = helmAwareLister{RawLister: forwardAwareLister{RawLister: cluster, forwards: sess.Forwards}}
		sess.Metrics = cluster
		sess.Location.Context = cluster.Context.ContextName
		sess.Location.Namespace = cluster.Context.Namespace
		sess.Registry, sess.Groups = resources.BuildDiscoveredRegistry(cluster.DiscoveredKinds(), cluster)

		return tui.ReplaceRootMsg{
			Task:        buildBrowseTask(cfg, sess, cluster),
			Events:      cluster.Events(),
			Conn:        cluster.ConnEvents(),
			BuildSetup:  buildSetupFactory(cfg, sess, cluster),
			BuildBrowse: buildBrowseFactory(cfg, sess, cluster),
		}
	}
}

// reconnectStartTimeout bounds attemptReconnect's initial cluster.Start
// (client build + informer cache sync) so an unreachable target doesn't
// hang the retry indefinitely — mirrors tui/context.go's
// switchContextTimeout for the same reason.
const reconnectStartTimeout = 15 * time.Second

// otherContexts lists kubeconfig context names other than current, for 4c's
// SWITCH CONTEXT preview.
func otherContexts(current string) []string {
	names, _, err := kube.AvailableContexts()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n != current {
			out = append(out, n)
		}
	}
	return out
}

// kubeconfigPathOrEmpty is kube.KubeconfigPath without the bool — setup
// just wants a display string, empty when it can't be determined.
func kubeconfigPathOrEmpty() string {
	if p, ok := kube.KubeconfigPath(); ok {
		return p
	}
	return ""
}

// openNodeDetailFunc pushes tasks/nodedetail (11b) for a node, wiring it
// against the same seams as browse. nodedetail's pod rows now open a real
// poddetail (5a) via openPodDetail rather than the log-stream stand-in
// nodedetail's own exit notes flagged — as a single-pod handoff (siblings
// = [pod.Name]), so poddetail's j/k is inert from this entry point; wiring
// real prev/next through nodedetail's own pod table is a natural follow-up,
// not required here.
func openNodeDetailFunc(sess *tui.Session, active seams, openPodDetail browse.OpenPodDetailFunc, openLogs browse.OpenLogsFunc, openYAML browse.OpenYAMLFunc, openExec func(namespace, name string, containers []kube.ContainerInfo, width, height int) (tea.Model, tea.Cmd), openForward browse.OpenForwardFunc) browse.OpenNodeDetailFunc {
	openPod := func(pod kube.Pod, width, height int) (tea.Model, tea.Cmd) {
		return openPodDetail(pod, []string{pod.Name}, 0, width, height)
	}
	openObjectEvents := openObjectEventsFunc(sess, active, openYAML)
	openObjectTimeline := openObjectTimelineFunc(sess, active, openObjectEvents)
	return func(nodeName string, width, height int) (tea.Model, tea.Cmd) {
		nd := nodedetail.New(nodedetail.Config{
			Session:      sess,
			Lister:       active,
			Metrics:      active,
			Mutator:      active,
			OpenPod:      nodedetail.OpenPodFunc(openPod),
			OpenLogs:     nodedetail.OpenLogsFunc(openLogs),
			OpenExec:     nodedetail.OpenExecFunc(openExec),
			OpenYAML:     nodedetail.OpenYAMLFunc(openYAML),
			OpenEvents:   nodedetail.OpenEventsFunc(openObjectEvents),
			OpenTimeline: nodedetail.OpenTimelineFunc(openObjectTimeline),
			OpenForward:  nodedetail.OpenForwardFunc(openForward),
			NodeName:     nodeName,
		})
		nd.SetSize(width, height)
		return &nd, nd.Init()
	}
}

// openPodDetailFunc pushes tasks/poddetail (5a) for a pod, wiring it against
// the same seams active already satisfies for browse/nodedetail.
func openPodDetailFunc(sess *tui.Session, active seams, openLogs browse.OpenLogsFunc, openYAML browse.OpenYAMLFunc, openExec func(namespace, name string, containers []kube.ContainerInfo, width, height int) (tea.Model, tea.Cmd), openForward browse.OpenForwardFunc) browse.OpenPodDetailFunc {
	openObjectEvents := openObjectEventsFunc(sess, active, openYAML)
	openObjectTimeline := openObjectTimelineFunc(sess, active, openObjectEvents)
	return func(pod kube.Pod, siblings []string, index int, width, height int) (tea.Model, tea.Cmd) {
		pd := poddetail.New(poddetail.Config{
			Session:      sess,
			Lister:       active,
			Metrics:      active,
			Events:       active,
			Mutator:      active,
			OpenLogs:     poddetail.OpenLogsFunc(openLogs),
			OpenYAML:     poddetail.OpenYAMLFunc(openYAML),
			OpenEvents:   poddetail.OpenEventsFunc(openObjectEvents),
			OpenTimeline: poddetail.OpenTimelineFunc(openObjectTimeline),
			OpenExec:     poddetail.OpenExecFunc(openExec),
			OpenForward:  poddetail.OpenForwardFunc(openForward),
			Namespace:    pod.Namespace,
			Name:         pod.Name,
			Siblings:     siblings,
			SiblingIndex: index,
		})
		pd.SetSize(width, height)
		return &pd, pd.Init()
	}
}

// openObjectDetailFunc pushes tasks/objectdetail (14d) for a discovered CRD
// kind's row, wiring it against the same seams active already satisfies for
// browse/nodedetail/poddetail.
func openObjectDetailFunc(sess *tui.Session, active seams, openYAML browse.OpenYAMLFunc) browse.OpenObjectDetailFunc {
	openObjectEvents := openObjectEventsFunc(sess, active, openYAML)
	return func(kind kube.ResourceKind, namespace, name string, siblings []string, index, width, height int) (tea.Model, tea.Cmd) {
		od := objectdetail.New(objectdetail.Config{
			Session:      sess,
			Lister:       active,
			Events:       active,
			Mutator:      active,
			OpenYAML:     objectdetail.OpenYAMLFunc(openYAML),
			OpenEvents:   objectdetail.OpenEventsFunc(openObjectEvents),
			Kind:         kind,
			Namespace:    namespace,
			Name:         name,
			Siblings:     siblings,
			SiblingIndex: index,
		})
		od.SetSize(width, height)
		return &od, od.Init()
	}
}

// openRouteTableFunc pushes tasks/routetable (23a/23b) for an Ingress or
// discovered Gateway API kind's row — active alone satisfies every seam it
// needs (RawLister for rows, YAMLReader for 'Y', plus its own object-scoped
// events push for 'e', the same openObjectEventsFunc objectdetail already
// uses).
func openRouteTableFunc(sess *tui.Session, active seams, openYAML browse.OpenYAMLFunc) browse.OpenRouteTableFunc {
	openEvents := openObjectEventsFunc(sess, active, openYAML)
	return func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd) {
		rt := routetable.New(routetable.Config{
			Session:    sess,
			Lister:     active,
			YAML:       active,
			OpenEvents: routetable.OpenEventsFunc(openEvents),
			Kind:       kind,
			Namespace:  namespace,
			Name:       name,
		})
		rt.SetSize(width, height)
		return &rt, rt.Init()
	}
}

// openWhoCanFunc pushes tasks/whocan (22a), pre-filled with verb/resource/
// namespace — active alone satisfies the WhoCanReader seam whocan needs.
func openWhoCanFunc(sess *tui.Session, active seams) browse.OpenWhoCanFunc {
	return func(verb, resource, namespace string, width, height int) (tea.Model, tea.Cmd) {
		wc := whocan.New(whocan.Config{
			Session:   sess,
			RBAC:      active,
			OpenYAML:  whocan.OpenYAMLFunc(openYAMLFunc(sess, active)),
			Verb:      verb,
			Resource:  resource,
			Namespace: namespace,
		})
		wc.SetSize(width, height)
		return &wc, wc.Init()
	}
}

// openYAMLFunc pushes tasks/yamlview (8a) for any object, any kind, wiring
// it against the same seams active already satisfies for browse/nodedetail/
// poddetail.
func openYAMLFunc(sess *tui.Session, active seams) browse.OpenYAMLFunc {
	return func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd) {
		yv := yamlview.New(yamlview.Config{
			Session:   sess,
			Lister:    active,
			YAML:      active,
			Kind:      kind,
			Namespace: namespace,
			Name:      name,
		})
		yv.SetSize(width, height)
		return &yv, yv.Init()
	}
}

// openHelmHistoryFunc pushes tasks/helmhistory (18a's 'h') for a release —
// active alone satisfies the RawLister/Mutator seams it needs (it reads
// Secrets directly, the same undecorated seam every real kind's own
// informer already backs, and rolls back through kube.Mutator.HelmRollback
// like every other write verb).
func openHelmHistoryFunc(sess *tui.Session, active seams) browse.OpenHelmHistoryFunc {
	return func(namespace, name string, width, height int) (tea.Model, tea.Cmd) {
		hh := helmhistory.New(helmhistory.Config{
			Session:   sess,
			Lister:    active,
			Mutator:   active,
			Namespace: namespace,
			Name:      name,
		})
		hh.SetSize(width, height)
		return &hh, hh.Init()
	}
}

// openSecretDataFunc pushes tasks/secretdata (27b) for a Secret row — active
// alone satisfies the RawLister/Mutator seams it needs (ListRaw for the
// Secret itself, kube.Mutator.PatchSecretData for add/remove).
func openSecretDataFunc(sess *tui.Session, active seams) browse.OpenSecretDataFunc {
	return func(namespace, name string, width, height int) (tea.Model, tea.Cmd) {
		sd := secretdata.New(secretdata.Config{
			Session:   sess,
			Lister:    active,
			Mutator:   active,
			Namespace: namespace,
			Name:      name,
		})
		sd.SetSize(width, height)
		return &sd, sd.Init()
	}
}

// openConfigMapDataFunc pushes tasks/configmapdata (27a) for a ConfigMap
// row — active alone satisfies the RawLister/Mutator seams it needs (ListRaw
// for the ConfigMap itself and its Deployment/StatefulSet/DaemonSet
// consumers, kube.Mutator.PatchConfigMapData/RolloutRestart for apply/ctrl-r).
func openConfigMapDataFunc(sess *tui.Session, active seams) browse.OpenConfigMapDataFunc {
	return func(namespace, name string, width, height int) (tea.Model, tea.Cmd) {
		cd := configmapdata.New(configmapdata.Config{
			Session:   sess,
			Lister:    active,
			Mutator:   active,
			Namespace: namespace,
			Name:      name,
		})
		cd.SetSize(width, height)
		return &cd, cd.Init()
	}
}

// helmValuesReader adapts an already-decoded kube.HelmRelease to yamlview's
// YAMLReader/RawLister seams — 18a's 'v' pushes 8a on data that was already
// fetched by browse's own load() (browse/helm.go's openSelectedHelmValues),
// not a fresh cluster round-trip, so this always answers the same release
// regardless of the (kind, namespace, name) yamlview's load() passes back.
// ListRaw returns a bare stand-in object just so yamlview's own
// findByName/ManagedFieldsYAML step (which every other kind needs to marshal
// managedFields) has something to find — a release has no managedFields of
// its own to show.
type helmValuesReader struct {
	release kube.HelmRelease
}

func (r helmValuesReader) ListRaw(_ context.Context, _ kube.ResourceKind, _ string) ([]runtime.Object, error) {
	return []runtime.Object{&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: r.release.Name, Namespace: r.release.Namespace}}}, nil
}

func (r helmValuesReader) GetYAML(_ context.Context, _ kube.ResourceKind, _, _ string) (string, string, error) {
	text := r.release.Values
	if text == "" {
		text = "# this release was installed with no values\n"
	}
	return text, fmt.Sprintf("revision %d", r.release.Revision), nil
}

// openHelmValuesFunc pushes tasks/yamlview (8a), read-only, on release's
// already-decoded values (docs/design README.md §18a: "v values in the YAML
// viewer, read-only").
func openHelmValuesFunc(sess *tui.Session) browse.OpenHelmValuesFunc {
	return func(release kube.HelmRelease, width, height int) (tea.Model, tea.Cmd) {
		reader := helmValuesReader{release: release}
		yv := yamlview.New(yamlview.Config{
			Session:   sess,
			Lister:    reader,
			YAML:      reader,
			Kind:      kube.KindHelmRelease,
			Namespace: release.Namespace,
			Name:      release.Name,
		})
		yv.SetSize(width, height)
		return &yv, yv.Init()
	}
}

// openExecFunc pushes tasks/execpicker (10a) for a pod's containers. Unlike
// this file's other Open*Func builders it needs no `active seams` argument
// — the picker only spawns a kubectl subprocess, it never reads from the
// cluster.
func openExecFunc(sess *tui.Session) func(namespace, name string, containers []kube.ContainerInfo, width, height int) (tea.Model, tea.Cmd) {
	return func(namespace, name string, containers []kube.ContainerInfo, width, height int) (tea.Model, tea.Cmd) {
		ep := execpicker.New(execpicker.Config{
			Session:    sess,
			Namespace:  namespace,
			PodName:    name,
			Containers: containers,
		})
		ep.SetSize(width, height)
		return &ep, ep.Init()
	}
}

// openForwardFunc pushes tasks/forwardpicker (13a) against a real cluster.
// Dialer/Resolver are built fresh from cluster.Clientset()/RESTConfig()
// inside the returned closure — not captured once here — so a forward
// started after a context switch always dials the currently active
// context, while an already-running forward (whose own dialer/resolver was
// snapshotted at its own Start call) keeps reconnecting against whichever
// context it was started under (docs/design README.md §13d: forwards
// "survive context switches").
func openForwardFunc(sess *tui.Session, lister resources.RawLister, cluster *kube.Cluster) browse.OpenForwardFunc {
	return func(target kube.ForwardTarget, width, height int) (tea.Model, tea.Cmd) {
		fp := forwardpicker.New(forwardpicker.Config{
			Session:  sess,
			Lister:   lister,
			Resolver: kube.NewClientsetPodResolver(cluster.Clientset()),
			Dialer:   kube.NewSpdyForwardDialer(cluster.Clientset(), cluster.RESTConfig()),
			Manager:  sess.Forwards,
			Target:   target,
		})
		fp.SetSize(width, height)
		return &fp, fp.Init()
	}
}

// openForwardFuncDemo is openForwardFunc's --demo counterpart, wired
// against kube/fake's stand-in dialer/resolver.
func openForwardFuncDemo(sess *tui.Session, lister resources.RawLister, mgr *kube.ForwardManager, demoCluster *fake.Cluster) browse.OpenForwardFunc {
	return func(target kube.ForwardTarget, width, height int) (tea.Model, tea.Cmd) {
		fp := forwardpicker.New(forwardpicker.Config{
			Session:  sess,
			Lister:   lister,
			Resolver: fake.NewPodResolver(demoCluster),
			Dialer:   fake.NewForwardDialer(),
			Manager:  mgr,
			Target:   target,
		})
		fp.SetSize(width, height)
		return &fp, fp.Init()
	}
}

// openEventsFunc pushes tasks/events (9b) namespace-scoped for browse's 'e'
// (and the goto palette's Events kind carve-out) — openYAML wires 9b's own
// 'y' the same way every other list/detail screen already gets it.
func openEventsFunc(sess *tui.Session, active seams, openYAML browse.OpenYAMLFunc) browse.OpenEventsFunc {
	return func(namespace string, width, height int) (tea.Model, tea.Cmd) {
		ev := events.New(events.Config{
			Session:   sess,
			Events:    active,
			Lister:    active,
			OpenYAML:  events.OpenYAMLFunc(openYAML),
			Namespace: namespace,
		})
		ev.SetSize(width, height)
		return &ev, ev.Init()
	}
}

// openObjectEventsFunc pushes tasks/events (9b) object-scoped for
// poddetail/nodedetail/objectdetail/routetable's 'e' — the underlying
// closure is reused by all four via an explicit named-type conversion at
// each call site (the same pattern OpenYAMLFunc already uses across
// browse/poddetail/nodedetail).
func openObjectEventsFunc(sess *tui.Session, active seams, openYAML browse.OpenYAMLFunc) func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd) {
	return func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd) {
		ev := events.New(events.Config{
			Session:    sess,
			Events:     active,
			Lister:     active,
			OpenYAML:   events.OpenYAMLFunc(openYAML),
			Namespace:  namespace,
			ObjectKind: kind,
			ObjectName: name,
		})
		ev.SetSize(width, height)
		return &ev, ev.Init()
	}
}

// openTimelineFunc pushes tasks/timeline (16a) namespace-scoped for
// browse's 't' — openEvents backs 16a's own 'e' (namespace-scoped, so
// timeline.OpenEventsFunc's kind/name args are dropped on the way through).
func openTimelineFunc(sess *tui.Session, active seams, openEvents browse.OpenEventsFunc) browse.OpenTimelineFunc {
	return func(namespace string, width, height int) (tea.Model, tea.Cmd) {
		tl := timeline.New(timeline.Config{
			Session:   sess,
			Events:    active,
			Lister:    active,
			Namespace: namespace,
			OpenEvents: func(_ kube.ResourceKind, namespace string, _ string, width, height int) (tea.Model, tea.Cmd) {
				return openEvents(namespace, width, height)
			},
		})
		tl.SetSize(width, height)
		return &tl, tl.Init()
	}
}

// openOverviewFunc pushes tasks/overview (19a) — reusing the same node-
// detail/timeline/events openers browse's own 'C'/'t'/'e' verbs already
// wire (openNodeDetail/openTimeline/openEvents), so 11b/16a/9b never drift
// between browse's row-level entry points and 19a's own NODES/`t`/`e` keys.
func openOverviewFunc(sess *tui.Session, lister resources.RawLister, nodeMetrics overview.NodeMetricsReader, openNodeDetail browse.OpenNodeDetailFunc, openTimeline browse.OpenTimelineFunc, openEvents browse.OpenEventsFunc) browse.OpenOverviewFunc {
	return func(width, height int) (tea.Model, tea.Cmd) {
		ov := overview.New(overview.Config{
			Session:        sess,
			Lister:         lister,
			NodeMetrics:    nodeMetrics,
			OpenNodeDetail: overview.OpenNodeDetailFunc(openNodeDetail),
			OpenTimeline:   overview.OpenTimelineFunc(openTimeline),
			OpenEvents:     overview.OpenEventsFunc(openEvents),
		})
		ov.SetSize(width, height)
		return &ov, ov.Init()
	}
}

// openObjectTimelineFunc pushes tasks/timeline (16b) object-scoped for
// poddetail/nodedetail's 't' — same named-type-conversion-at-call-site
// pattern openObjectEventsFunc already establishes. openEvents backs 16b's
// own 'e', reusing the same object-scoped closure poddetail/nodedetail's
// own OpenEvents is built from.
func openObjectTimelineFunc(sess *tui.Session, active seams, openEvents func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd)) func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd) {
	return func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd) {
		tl := timeline.New(timeline.Config{
			Session:    sess,
			Events:     active,
			Lister:     active,
			Namespace:  namespace,
			ObjectKind: kind,
			ObjectName: name,
			OpenEvents: timeline.OpenEventsFunc(openEvents),
		})
		tl.SetSize(width, height)
		return &tl, tl.Init()
	}
}

// liveClusterLogStreamer re-resolves cluster's clientset on every stream
// request instead of capturing it once at wiring time. Necessary now that
// contexts can switch at runtime (7a, mvp-plan.md Phase 3):
// Cluster.SwitchContext rebuilds the clientset in place on the same
// *kube.Cluster pointer, so a kube.ClientPodLogStreamer built from a single
// cluster.Clientset() snapshot at startup would keep streaming against the
// pre-switch cluster forever.
type liveClusterLogStreamer struct {
	cluster *kube.Cluster
}

func (s liveClusterLogStreamer) StreamPodLogs(ctx context.Context, req kube.LogStreamRequest) (io.ReadCloser, error) {
	return kube.ClientPodLogStreamer{Client: s.cluster.Clientset()}.StreamPodLogs(ctx, req)
}

func openLogsFunc(sess *tui.Session, lister resources.RawLister, streamer kube.PodLogStreamer, clusterName, namespace string) browse.OpenLogsFunc {
	return func(pod kube.Pod, width, height int) (tea.Model, tea.Cmd) {
		if pod.Context == "" {
			pod.Context = clusterName
		}
		if pod.Namespace == "" {
			pod.Namespace = namespace
		}
		logs := podlogs.FromPod(sess, lister, pod, streamer)
		logs.SetSize(width, height)
		return &logs, logs.Start()
	}
}

func Run() error {
	return RunWithConfig(DefaultConfig())
}

// RunWithConfig runs the program with cfg (--demo/--theme wired in from
// flags by the caller). It pipes both resource-change and connection-state
// events into the program (real cluster or --demo fake, whichever is
// active) and saves session state on exit.
func RunWithConfig(cfg Config) error {
	silenceKlog()
	model, cluster, demoCluster := NewModel(cfg)
	program := tea.NewProgram(model)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	switch {
	case cluster != nil:
		defer cluster.Stop()
		go forwardEvents(ctx, cluster, program)
		go func() {
			_ = cluster.Start(ctx)
			// The one connect path where CRD discovery (folded into
			// Start) finishes outside any tea.Cmd the root can await
			// synchronously — see kube.CRDsDiscoveredMsg's doc comment.
			// Harmless to send even if Start failed: DiscoveredKinds()
			// is just empty then.
			program.Send(kube.CRDsDiscoveredMsg{})
		}()
	case demoCluster != nil:
		go forwardEvents(ctx, demoCluster, program)
	}
	if sess := model.Session(); sess != nil && sess.Forwards != nil {
		go watchForwardManager(ctx, sess.Forwards, program)
	}
	if sess := model.Session(); sess != nil {
		if cmd := updateCheckCmd(sess, buildChecker(cfg), false); cmd != nil {
			// Same "result arrives outside any tea.Cmd the root can await
			// synchronously" shape as the CRDsDiscoveredMsg goroutine just
			// above — updateCheckCmd's own tea.Cmd already does the
			// network round trip off-thread, so calling it directly here
			// (rather than threading it through Model.Init) doesn't block
			// program startup.
			go func() { program.Send(cmd()) }()
		}
	}

	_, err := program.Run()

	if sess := model.Session(); sess != nil {
		sess.SyncLocationToPerContext()
		_ = sess.State.Save()
	}
	return err
}

// silenceKlog routes client-go's logger (klog) away from stderr, where its
// output — client-side throttling warnings, reflector errors — would splatter
// over the Bubble Tea screen. This also covers everything routed through
// utilruntime.ErrorHandlers, which logs via klog.
func silenceKlog() {
	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	_ = fs.Set("logtostderr", "false")
	_ = fs.Set("alsologtostderr", "false")
	_ = fs.Set("stderrthreshold", "FATAL")
	klog.SetOutput(io.Discard)
}

// watchForwardManager bridges mgr's change notifications into the Bubble
// Tea program as a kube.ResourceChangedMsg{Kind: KindForward} — reusing the
// exact message type/path every other kind's watch events already flow
// through, so browse's Forwards row reloads without any bespoke message or
// Update case (docs/design README.md §13c).
func watchForwardManager(ctx context.Context, mgr *kube.ForwardManager, program *tea.Program) {
	events := mgr.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			program.Send(kube.ResourceChangedMsg{Kind: kube.KindForward})
		}
	}
}

// eventSource is what forwardEvents needs, satisfied by both *kube.Cluster
// and *fake.Cluster.
type eventSource interface {
	Events() <-chan kube.ResourceChangedMsg
	ConnEvents() <-chan kube.ConnStateMsg
}

func forwardEvents(ctx context.Context, src eventSource, program *tea.Program) {
	events := src.Events()
	conn := src.ConnEvents()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			program.Send(ev)
			if ev.Kind == kube.KindSecret {
				// helmAwareLister computes KindHelmRelease from the Secret
				// cache rather than its own informer (docs/design README.md
				// §18a), so a Secret change needs to also invalidate any
				// open Helm Releases/history screen — they key their own
				// reload off KindHelmRelease/KindSecret, never KindSecret
				// alone reaching them by coincidence.
				program.Send(kube.ResourceChangedMsg{Kind: kube.KindHelmRelease})
			}
		case cs, ok := <-conn:
			if !ok {
				conn = nil
				continue
			}
			program.Send(cs)
		}
	}
}
