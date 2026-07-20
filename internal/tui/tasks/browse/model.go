// Package browse is the inverted-layout main table: the one resting screen
// for every resource kind (mvp-plan.md §Phase 1, docs/design/README.md
// screens 2a/10c). It replaced the pre-redesign root/per-kind-list/
// resource-list screens as the app's root task.
//
// This covers the Chrome v2 Screen contract, loading/ready/empty states,
// the 10c empty-namespace explainer, the 2a health strip, filter, default
// unhealthy-first sort, live CPU/MEM bars for pods, and pushing the
// existing podlogs screen on 'l'. Row-open navigation to a detail/YAML view
// and mutating verbs (delete, …) land in later phases.
package browse

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/components"
)

// pollInterval is the metrics/sync poll cadence (the header's "sync 2s"
// chip, mvp-plan.md's Defaults assumed).
const pollInterval = 2 * time.Second

// reloadDebounce coalesces bursts of watch events into one reload (Phase 1:
// "watch events arrive in bursts").
const reloadDebounce = 250 * time.Millisecond

// MetricsReader is the live pod-usage seam browse needs for the CPU/MEM
// mini-bars — satisfied by *kube.Cluster and *fake.Cluster.
type MetricsReader interface {
	PodMetricsByNamespace(ctx context.Context, namespace string) (map[string]kube.PodMetrics, error)
}

// OpenLogsFunc pushes the log-stream screen for pod, mirroring the
// pre-redesign pods screen's equivalent shape so app.go's existing streamer
// wiring carries over unchanged.
type OpenLogsFunc func(pod kube.Pod, width, height int) (tea.Model, tea.Cmd)

// ConnRetrier lets browse trigger an immediate reconnect probe on 'r' while
// offline (4a), bypassing the exponential backoff wait — satisfied by
// *kube.Cluster and *fake.Cluster.
type ConnRetrier interface {
	RetryNow()
}

// CacheSyncChecker is optionally implemented by a Lister whose cache
// populates asynchronously (*kube.Cluster's informers, right after launch or
// mid SwitchContext). ListRaw reads the cache regardless of sync state, so
// an empty rowsLoadedMsg during that window looks identical to a genuinely
// empty namespace; applyRowsLoaded type-asserts for this to tell them apart
// instead of flashing the empty state before the first real list lands.
// *fake.Cluster doesn't implement it — its cache is always populated
// synchronously, so the type assertion just falls through to "synced".
type CacheSyncChecker interface {
	Synced() bool
}

// OpenNodeDetailFunc pushes tasks/nodedetail (11b) for the named node.
type OpenNodeDetailFunc func(nodeName string, width, height int) (tea.Model, tea.Cmd)

// OpenPodDetailFunc pushes tasks/poddetail (5a) for pod. siblings/index are
// the current visible list's ordered pod names + the selected row's
// position, so poddetail's j/k can move to the next/prev pod without
// leaving detail (mvp-plan.md §5a).
type OpenPodDetailFunc func(pod kube.Pod, siblings []string, index int, width, height int) (tea.Model, tea.Cmd)

// OpenYAMLFunc pushes tasks/yamlview (8a) for the named object of kind —
// works for any kind, unlike OpenLogsFunc/OpenPodDetailFunc which are
// Pod-only (docs/design README.md: "y opens the YAML view on any selected
// object, any kind").
type OpenYAMLFunc func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd)

// OpenEventsFunc pushes tasks/events (9b) namespace-scoped for browse's 'e'
// (docs/design README.md §9b: "reached by e from browse, namespace-scoped")
// — namespace == "" mirrors 6b's all-namespaces triage rather than a
// separate mode.
type OpenEventsFunc func(namespace string, width, height int) (tea.Model, tea.Cmd)

// OpenTimelineFunc pushes tasks/timeline (16a) namespace-scoped for
// browse's 't' (docs/design README.md §16a) — namespace == "" mirrors 6b's
// all-namespaces triage, same rule OpenEventsFunc already follows.
type OpenTimelineFunc func(namespace string, width, height int) (tea.Model, tea.Cmd)

// OpenExecFunc pushes tasks/execpicker (10a) for a pod with more than one
// container — browse execs single-container pods directly via
// kube.ExecSpec without pushing a task (docs/design README.md §10a:
// "skipped entirely for single-container pods").
type OpenExecFunc func(namespace, name string, containers []kube.ContainerInfo, width, height int) (tea.Model, tea.Cmd)

// OpenForwardFunc pushes tasks/forwardpicker (13a) for a Pod/Service/
// Deployment row (docs/design README.md §13a).
type OpenForwardFunc func(target kube.ForwardTarget, width, height int) (tea.Model, tea.Cmd)

// OpenObjectDetailFunc pushes tasks/objectdetail (14d) for a discovered CRD
// kind's row (resources.Descriptor.Custom) — siblings/index are the current
// visible list's ordered names + the selected row's position, same shape as
// OpenPodDetailFunc, so objectdetail's j/k can move without leaving detail.
type OpenObjectDetailFunc func(kind kube.ResourceKind, namespace, name string, siblings []string, index, width, height int) (tea.Model, tea.Cmd)

// OpenRouteTableFunc pushes tasks/routetable (23a/23b) for an Ingress row or
// a discovered Gateway API kind's row (HTTPRoute/GRPCRoute/TCPRoute/Gateway)
// — see routes.go's isRouteKind.
type OpenRouteTableFunc func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd)

// OpenWhoCanFunc pushes tasks/whocan (22a), pre-filled with verb/resource/
// namespace — the `g "who"` goto entry (kube.KindWhoCan, handled by
// switchKind's carve-out below rather than an ordinary registry list, since
// there's nothing to list) and the 4b 403 card's 'w' recovery key (denied
// verb/resource/namespace pre-filled, docs/design README.md §22a: "arriving
// with the failed verb+resource pre-filled").
type OpenWhoCanFunc func(verb, resource, namespace string, width, height int) (tea.Model, tea.Cmd)

// OpenHelmHistoryFunc pushes tasks/helmhistory (18a's `h`) for a release's
// full revision rail.
type OpenHelmHistoryFunc func(namespace, name string, width, height int) (tea.Model, tea.Cmd)

// OpenHelmValuesFunc pushes tasks/yamlview (18a's `v`) read-only on
// release's decoded values.
type OpenHelmValuesFunc func(release kube.HelmRelease, width, height int) (tea.Model, tea.Cmd)

// OpenOverviewFunc pushes tasks/overview (19a) — the `g "ov"` goto entry
// (kube.KindOverview, handled by switchKind's carve-out below, the same
// shape as OpenWhoCanFunc's KindWhoCan carve-out since there's nothing to
// list).
type OpenOverviewFunc func(width, height int) (tea.Model, tea.Cmd)

// Config are the dependencies browse needs, per repo convention
// (package-local Config struct, interface-typed fields, New fills zero
// values).
type Config struct {
	Session          *tui.Session
	Lister           resources.RawLister
	Metrics          MetricsReader
	NodeMetrics      NodeMetricsReader
	Mutator          kube.Mutator
	OpenLogs         OpenLogsFunc
	OpenNodeDetail   OpenNodeDetailFunc
	OpenPodDetail    OpenPodDetailFunc
	OpenYAML         OpenYAMLFunc
	OpenEvents       OpenEventsFunc
	OpenTimeline     OpenTimelineFunc
	OpenExec         OpenExecFunc
	OpenForward      OpenForwardFunc
	OpenObjectDetail OpenObjectDetailFunc
	OpenRouteTable   OpenRouteTableFunc
	OpenWhoCan       OpenWhoCanFunc
	OpenHelmHistory  OpenHelmHistoryFunc
	OpenHelmValues   OpenHelmValuesFunc
	OpenOverview     OpenOverviewFunc
	// Forwards is the app-wide port-forward registry (13c's stop/restart/
	// stop-all verbs act on it directly, unlike OpenForward's picker push) —
	// nil disables those verbs the same way a nil Mutator disables delete.
	Forwards    *kube.ForwardManager
	Retrier     ConnRetrier
	LoadTimeout time.Duration
	// InitErr is the reason Lister is nil (cluster unreachable at launch) —
	// surfaced verbatim instead of a generic message when set. Phase 4's
	// tasks/setup screen replaces this with the real 4c/10b recovery flow.
	InitErr error
}

type Model struct {
	width, height int

	session          *tui.Session
	lister           resources.RawLister
	metrics          MetricsReader
	nodeMetricsSrc   NodeMetricsReader
	mutator          kube.Mutator
	actions          actions.Controller
	openLogs         OpenLogsFunc
	openNodeDetail   OpenNodeDetailFunc
	openPodDetail    OpenPodDetailFunc
	openYAML         OpenYAMLFunc
	openEvents       OpenEventsFunc
	openTimeline     OpenTimelineFunc
	openExec         OpenExecFunc
	openForward      OpenForwardFunc
	openObjectDetail OpenObjectDetailFunc
	openRouteTable   OpenRouteTableFunc
	openWhoCan       OpenWhoCanFunc
	openHelmHistory  OpenHelmHistoryFunc
	openHelmValues   OpenHelmValuesFunc
	openOverview     OpenOverviewFunc
	forwards         *kube.ForwardManager
	retrier          ConnRetrier
	timeout          time.Duration
	// execFeedback carries a non-zero kubectl-exec exit's message (docs/design
	// README.md §10a: "exit returns to the same pod with a feedback line on
	// non-zero exit") — single-container pods exec directly from browse
	// without pushing execpicker, so browse needs its own copy of this
	// transient state rather than relying on the picker's. Also carries
	// kubectl edit's exit message (editResultMsg, edit.go) and node shell's
	// (nodeShellResultMsg) — same transient channel, same reasoning.
	execFeedback string
	// pendingEdit is non-nil while 'E' edit's PROD-only y/N line is showing
	// (edit.go's bespoke gate — verbs.TierForEdit) — nil the rest of the
	// time, including right after a non-prod 'E' press, which skips this
	// state entirely and launches kubectl edit directly.
	pendingEdit *editTarget
	// pendingStopAllForwards is true while 13c's "stop all" (X) inline y/N
	// is showing — the same bespoke (non-actions.Controller) gate shape as
	// pendingEdit, since stopping local forward sessions isn't a
	// kube.Mutator operation (browse/forwards.go).
	pendingStopAllForwards bool
	// pendingScale is non-nil while 17b's +/− numeric prompt is showing
	// (scale.go) — a bespoke gate like pendingEdit, since Scale needs a
	// typed-ahead replica count gathered before there's an action to Begin,
	// rather than actions.Controller's own y/N/type-name confirmation shapes.
	pendingScale *scaleTarget
	// pendingSetImage is non-nil while 24a's inline set-image/set-tag panel
	// is showing (setimage.go) — a bespoke gate like pendingScale, since
	// there's a container/tag/history buffer to gather before there's an
	// action to Begin.
	pendingSetImage *setImageTarget
	// marks is 20a's marked set (bulk.go), keyed by markKey(namespace, name)
	// so 6b's cross-namespace grouped view can't collide two same-named rows
	// in different namespaces. nil/empty means no marks — every marks-aware
	// render path (health strip, mode pill, table's mark column) must render
	// zero chrome in that case (13d's rule).
	marks map[string]bool
	// pendingBulkDelete is non-nil while 20a's bulk-delete confirm is
	// showing — a bespoke gate like pendingScale/pendingEdit, since a bulk
	// action has no single ResourceName for actions.Controller's own
	// y/N/type-name shapes to key off.
	pendingBulkDelete *bulkDeleteTarget

	kind      kube.ResourceKind
	namespace string
	desc      resources.Descriptor

	rows      []resources.Row
	fetchedAt time.Time
	// rowCache holds the last successfully-loaded rows per kind+namespace
	// seen this session (browseCacheKey), so revisiting one mid-session can
	// show cached rows dimmed instead of the skeleton loader (docs/design
	// README.md §15a: "revisiting a kind seen this session: cached rows
	// dimmed instead of skeletons" — the same muted treatment 4a's stale
	// grammar already gives an offline table). Never cleared by
	// resetAndLoad — it outlives any single kind/namespace view on purpose.
	rowCache map[browseCacheKey][]resources.Row
	// cachedView is true while m.state is TaskStateLoading but m.rows holds
	// a rowCache hit rather than fresh data — Body()/tableBody read it to
	// render the muted cached snapshot instead of the skeleton, and it's
	// cleared the moment a real rowsLoadedMsg lands (applyRowsLoaded).
	cachedView bool
	// nodeCount backs the health strip's "36 pods · 3 nodes" right side
	// (Pods kind only; 0 = unknown, suffix omitted).
	nodeCount int
	// conn is the last kube.ConnStateMsg forwarded by the root shell — the
	// header badge's "· 12ms" latency, and (once Phase is Reconnecting/
	// Failed) the 4a offline banner/stale-strip/muted-table treatment. Zero
	// until the first ping completes.
	conn kube.ConnState
	// now is the wall-clock time as of the last kube.ConnStateMsg — 4a's
	// "next in Ns"/"NNs old" figures are computed from this rather than a
	// clock read inside Render (render must stay pure: f(model, theme,
	// size)). It ticks roughly every pingInterval while offline, since the
	// health ping loop re-emits ConnStateMsg on every attempt.
	now time.Time
	// podMetrics is nil until the first successful poll (browse renders "–"
	// bars until then); it's namespace-scoped like rows, so pod names alone
	// key it safely.
	podMetrics map[string]kube.PodMetrics
	// pods is the fuller kube.PodFromObject projection (Pod kind only) —
	// Row only carries display Cells, but the 'l' logs verb needs each
	// pod's container names.
	pods map[string]kube.Pod
	// helmReleases is the fuller decoded kube.HelmRelease (HelmRelease kind
	// only), keyed by release name — Row only carries display Cells, but
	// 'v'/'h'/'R' need the chart/values/revision fields it doesn't.
	helmReleases map[string]kube.HelmRelease

	// nodeMetrics/nodeCapacity/podCountByNode/clusterPodTotal back 11a's
	// Nodes columns and health-strip right side (Node kind only) — see
	// nodes.go's loadNodeExtras and nodeMetricCell/nodePodsCell.
	nodeMetrics     map[string]kube.NodeMetric
	nodeCapacity    map[string]nodeCapacity
	podCountByNode  map[string]int
	clusterPodTotal int

	visible []filterMatch // rows after the filter, before 6b's fold/collapse
	// expandedGroups tracks which 6b namespace groups the user has manually
	// expanded past their triage-default collapsed state (grouping.go's
	// buildDisplayRows) — absent/false means collapsed. Reset by
	// resetAndLoad alongside the rest of the per-view UI state below, since
	// it's meaningless once the kind/namespace/context changes.
	expandedGroups map[string]bool
	// display is visible expanded into 6b's render/selection list — one
	// entry per rendered components.Row, including synthetic group header/
	// fold/collapsed-summary lines (grouping.go's buildDisplayRows).
	// selected indexes THIS list, not visible — every entry maps 1:1 to one
	// Table.Rows entry, so Table.Selected can just be set to selected
	// directly, with no separate Rows-space translation (grouping.go used
	// to carry groupBoundaries/rowsPosition for exactly that, before this
	// field replaced them).
	display  []displayRow
	selected int
	offset   int
	// pendingSelect names a row to select once the next rowsLoadedMsg lands
	// — set by a jump-palette resource Enter (tui.GotoResourceMsg) that
	// switched kind, consumed by recomputeVisible.
	pendingSelect string

	filterActive bool
	filterQuery  string

	// originKind/originName name the row whose filtered child view (a
	// Deployment's pods, openDeploymentPods; a Helm release's objects,
	// openReleaseObjects) is currently showing — when originName is
	// non-empty, esc jumps back to that row on originKind instead of popping
	// the task stack, and Header()'s breadcrumb names it. Cleared by every
	// navigation path that isn't "back to the origin row" (see clearOrigin)
	// so esc can't misfire once the user has moved on to something else.
	originKind kube.ResourceKind
	originName string

	// reloadEpoch/metricsEpoch guard debounced/ticked reloads against a
	// kind switch mid-flight (Phase 2): a scheduled tick only acts if the
	// epoch it captured is still current.
	reloadEpoch  int
	metricsEpoch int

	state    tui.TaskState
	feedback string
	spinner  components.Spinner
	// loadStartedAt is when the current TaskStateLoading spell began — 15a's
	// header timer ("◐ loading pods · 0.4s") measures elapsed against this
	// rather than reading the clock in Render (render must stay pure:
	// f(model, theme, size)); SpinnerTickMsg's 100ms cadence keeps m.now
	// fresh while it's ticking.
	loadStartedAt time.Time

	hints emptyHints
}

// rowsLoadedMsg carries a kind's freshly-listed rows. kind guards against a
// reply for a since-superseded kind (relevant once the jump palette can
// switch kinds mid-flight, Phase 2). nodeCount rides along for the Pods
// health strip (0 when unknown or not the Pods kind); nodeCapacity/
// podCountByNode/clusterPodTotal ride along for the Nodes columns/health
// strip (Node kind only, see nodes.go's loadNodeExtras).
type rowsLoadedMsg struct {
	kind            kube.ResourceKind
	rows            []resources.Row
	pods            map[string]kube.Pod
	helmReleases    map[string]kube.HelmRelease
	nodeCount       int
	nodeCapacity    map[string]nodeCapacity
	podCountByNode  map[string]int
	clusterPodTotal int
	err             error
}

// emptyHintsMsg carries the 10c ways-out data computed once rows come back
// empty.
type emptyHintsMsg struct {
	kind  kube.ResourceKind
	hints emptyHints
}

// reloadDueMsg fires reloadDebounce after a ResourceChangedMsg; epoch guards
// against acting on a stale (superseded) debounce timer.
type reloadDueMsg struct{ epoch int }

// metricsTickMsg drives the 2s metrics poll loop; epoch guards it the same
// way reloadDueMsg guards reload debouncing.
type metricsTickMsg struct{ epoch int }

// podMetricsLoadedMsg carries one poll's result. epoch/namespace guard
// against a reply for a since-superseded namespace/kind.
type podMetricsLoadedMsg struct {
	epoch     int
	namespace string
	metrics   map[string]kube.PodMetrics
	err       error
}

func New(cfg Config) Model {
	if cfg.LoadTimeout == 0 {
		cfg.LoadTimeout = 10 * time.Second
	}

	kind := kube.KindPod
	namespace := "default"
	var reg resources.Registry
	if cfg.Session != nil {
		reg = cfg.Session.Registry
		namespace = cfg.Session.Location.Namespace
		if cfg.Session.Location.Kind != "" {
			kind = cfg.Session.Location.Kind
		}
	}
	// A persisted Location.Kind can name a CRD kind that isn't in reg yet
	// (real-cluster startup builds browse before discovery has run, so
	// Session.Registry is still resources.DefaultRegistry's built-ins only)
	// — falling back to Pod here, like switchKind already does for the same
	// "unknown kind" case, avoids handing load() a zero-value Descriptor
	// (empty Kind) that ListRaw has no informer for.
	desc, ok := reg.Descriptor(kind)
	if !ok {
		kind = kube.KindPod
		desc, _ = reg.Descriptor(kind)
	}

	state := tui.TaskStateLoading
	feedback := "Loading " + desc.Display + "..."
	if cfg.Lister == nil {
		state = tui.TaskStateError
		feedback = "no cluster connection"
		if cfg.InitErr != nil {
			feedback = cfg.InitErr.Error()
		}
	}

	return Model{
		width:            tui.DefaultWidth,
		height:           tui.DefaultHeight,
		session:          cfg.Session,
		lister:           cfg.Lister,
		metrics:          cfg.Metrics,
		nodeMetricsSrc:   cfg.NodeMetrics,
		mutator:          cfg.Mutator,
		actions:          actions.New(cfg.Mutator),
		openLogs:         cfg.OpenLogs,
		openNodeDetail:   cfg.OpenNodeDetail,
		openPodDetail:    cfg.OpenPodDetail,
		openYAML:         cfg.OpenYAML,
		openEvents:       cfg.OpenEvents,
		openTimeline:     cfg.OpenTimeline,
		openExec:         cfg.OpenExec,
		openForward:      cfg.OpenForward,
		openObjectDetail: cfg.OpenObjectDetail,
		openRouteTable:   cfg.OpenRouteTable,
		openWhoCan:       cfg.OpenWhoCan,
		openHelmHistory:  cfg.OpenHelmHistory,
		openHelmValues:   cfg.OpenHelmValues,
		openOverview:     cfg.OpenOverview,
		forwards:         cfg.Forwards,
		retrier:          cfg.Retrier,
		timeout:          cfg.LoadTimeout,
		kind:             kind,
		namespace:        namespace,
		desc:             desc,
		state:            state,
		feedback:         feedback,
		now:              time.Now(),
		loadStartedAt:    time.Now(),
	}
}

// grouped reports whether the table renders 6b's namespace-grouped triage
// view: all-namespaces mode (namespace == "") on a namespaced kind.
// Cluster-scoped kinds (Nodes, Namespaces) always pass namespace="" to
// ListRaw regardless of m.namespace (see countNamespace), so they're
// excluded — there's no namespace to group by.
func (m Model) grouped() bool {
	return m.namespace == "" && !m.desc.ClusterScoped
}

// listerSynced reports whether m.lister's cache is done with its initial
// sync — true for any lister that doesn't opt into CacheSyncChecker (fakes,
// test doubles), so this only changes behavior for *kube.Cluster.
func (m Model) listerSynced() bool {
	sc, ok := m.lister.(CacheSyncChecker)
	return !ok || sc.Synced()
}

// pollsMetrics reports whether kind's CPU/MEM columns need the 2s metrics
// poll — Pods (pod usage) and Nodes (11a's live cluster/node bars) today.
func (m Model) pollsMetrics() bool {
	switch m.kind {
	case kube.KindPod:
		return m.metrics != nil
	case kube.KindNode:
		return m.nodeMetricsSrc != nil
	default:
		return false
	}
}

// offline reports whether the 4a treatment applies: the last known
// connection state is mid-outage (watch/ping failing, with backoff
// retries under way) rather than a one-shot failure. The table/rows
// themselves are untouched — browsing the stale snapshot still works.
func (m Model) offline() bool {
	return m.conn.Offline()
}

func (m Model) Init() tea.Cmd {
	if m.lister == nil {
		return nil
	}
	cmds := []tea.Cmd{m.load(), components.SpinnerTick()}
	if m.pollsMetrics() {
		cmds = append(cmds, m.loadMetricsCmd(m.metricsEpoch), m.scheduleMetricsTick(m.metricsEpoch))
	}
	return tea.Batch(cmds...)
}

func (m *Model) SetSize(width, height int) {
	size := tui.NormalizeSize(width, height)
	m.width = size.Width
	m.height = size.Height
}

// countNamespace is the namespace argument for List/Count calls against the
// current kind: cluster-scoped kinds (Nodes, Namespaces) ignore the active
// namespace entirely.
func (m Model) countNamespace() string {
	if m.desc.ClusterScoped {
		return ""
	}
	return m.namespace
}

func (m Model) load() tea.Cmd {
	lister := m.lister
	desc := m.desc
	ns := m.countNamespace()
	kind := m.kind
	timeout := m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		rows, err := resources.List(ctx, lister, desc, ns)
		if err != nil {
			return rowsLoadedMsg{kind: kind, err: err}
		}
		var pods map[string]kube.Pod
		var helmReleases map[string]kube.HelmRelease
		nodeCount := 0
		var nodeCap map[string]nodeCapacity
		var podCountByNode map[string]int
		clusterPodTotal := 0
		switch kind {
		case kube.KindPod:
			pods = podsByName(ctx, lister, ns)
			// Best-effort node count for the health strip's "· 3 nodes"
			// suffix — 0 on error just omits it.
			if n, err := resources.Count(ctx, lister, kube.KindNode, ""); err == nil {
				nodeCount = n
			}
		case kube.KindNode:
			nodeCap, podCountByNode, clusterPodTotal = loadNodeExtras(ctx, lister)
		case kube.KindHelmRelease:
			helmReleases = helmReleasesByName(ctx, lister, ns)
		}
		return rowsLoadedMsg{
			kind: kind, rows: rows, pods: pods, helmReleases: helmReleases, nodeCount: nodeCount,
			nodeCapacity: nodeCap, podCountByNode: podCountByNode, clusterPodTotal: clusterPodTotal,
		}
	}
}

// switchKind mutates kind in place (mvp-plan.md Phase 2: "no stack push
// between kinds — jumping kinds mutates the browse model"), keeping the
// current namespace, and re-issues load()/metrics setup. A no-op (nil cmd)
// for an unknown kind or the kind already showing.
// switchKind mutates kind in place. Like switchNamespace below, it also
// mirrors Session.Location.Kind — reached both via the root-forwarded
// tui.GotoKindMsg (idempotent: the root already set Location.Kind before
// forwarding) and via direct internal calls that bypass the root entirely
// (openDeploymentPods/openReleaseObjects's "↵ = this row's filtered pods",
// and their own esc-back) — the same staleness switchNamespace's doc
// comment describes, just for the Kind dimension instead of Namespace.
func (m *Model) switchKind(kind kube.ResourceKind) tea.Cmd {
	if m.session == nil || kind == m.kind {
		return nil
	}
	desc, ok := m.session.Registry.Descriptor(kind)
	if !ok {
		return nil
	}
	m.kind = kind
	m.desc = desc
	m.session.Location.Kind = kind
	return m.resetAndLoad()
}

// switchNamespace mutates namespace in place, keeping kind and filter, and
// re-issues load()/metrics setup. A no-op for the namespace already active.
// resetAndLoad normally clears the filter along with the rest of the
// per-view state (right for a kind switch, whose query no longer means
// anything against a different kind's names), so a namespace switch snapshots
// filterQuery/filterActive first and restores them after — docs/design
// README.md §6a: "switching keeps kind + filter".
//
// Mirrors Session.Location.Namespace the same way setFilter mirrors
// Location.Filter: this is the only place browse mutates m.namespace, but
// it's reached two ways — forwarded from the root shell's own
// tui.SwitchNamespaceMsg handling (which already set Location.Namespace
// before forwarding, so this is idempotent there) and two hotkeys that
// bypass the root entirely ('a' all-namespaces, 'N' jump-into-namespace,
// update.go) which previously left Location.Namespace stale. That staleness
// was invisible in browse's own view (which reads m.namespace directly) but
// broke the goto palette's live per-kind counts (tui/goto.go's
// gotoNamespace reads Location.Namespace) after either hotkey — a `g`
// right after 'a' kept scoping counts to the namespace you'd left, even
// though the header already said "∗ all namespaces".
func (m *Model) switchNamespace(namespace string) tea.Cmd {
	if namespace == m.namespace {
		return nil
	}
	m.namespace = namespace
	if m.session != nil {
		m.session.Location.Namespace = namespace
	}
	query, active := m.filterQuery, m.filterActive
	cmd := m.resetAndLoad()
	m.filterActive = active
	m.setFilter(query)
	return cmd
}

// setFilter updates the live filter query, keeping Session.Location.Filter
// (the cross-screen mirror context.go's switchContextCmd reads to persist
// into state.PerContext and writes back on restore — mvp-tasks.md's
// "PerContext.Filter exists but nothing writes it" gap) in sync. Every
// filterQuery mutation goes through this instead of assigning the field
// directly.
func (m *Model) setFilter(query string) {
	m.filterQuery = query
	if m.session != nil {
		m.session.Location.Filter = query
	}
}

// goToResource handles a jump-to-resource navigation: switch kind and/or
// namespace first if the target lives in either a different one (selecting
// it once rows land, via pendingSelect), otherwise select it immediately
// since rows are already loaded.
//
// The namespace switch matters for cluster-wide jumps — tasks/overview's
// TROUBLE/RECENT CHANGES panels aggregate pods/rollouts across every
// namespace (19a), so ↵ there can land on an object outside whatever
// namespace browse currently has active. Without switching, the target
// object simply never appears in the freshly namespace-scoped load and
// pendingSelect silently finds nothing to select.
func (m *Model) goToResource(msg tui.GotoResourceMsg) tea.Cmd {
	m.clearOrigin()
	m.pendingSelect = msg.Name

	if m.session == nil {
		m.recomputeVisible()
		return nil
	}

	desc := m.desc
	kindChanged := msg.Kind != m.kind
	if kindChanged {
		d, ok := m.session.Registry.Descriptor(msg.Kind)
		if !ok {
			return nil
		}
		desc = d
	}
	namespaceChanged := !desc.ClusterScoped && msg.Namespace != "" && msg.Namespace != m.namespace

	if !kindChanged && !namespaceChanged {
		m.recomputeVisible()
		return nil
	}
	if kindChanged {
		m.kind = msg.Kind
		m.desc = desc
		m.session.Location.Kind = msg.Kind
	}
	if namespaceChanged {
		m.namespace = msg.Namespace
		m.session.Location.Namespace = msg.Namespace
	}
	return m.resetAndLoad()
}

// clearOrigin drops the "esc back to origin row" state (originKind/
// originName) — called from every navigation path that isn't the esc-back
// one itself, so a stale target can't resurface after the user has moved on
// to something else.
func (m *Model) clearOrigin() {
	m.originKind = ""
	m.originName = ""
}

// browseCacheKey identifies one kind+namespace view for rowCache — the same
// two dimensions switchKind/switchNamespace mutate, and the same pairing
// applyRowsLoaded's own stale-reply guard (msg.kind != m.kind) already keys
// off implicitly.
type browseCacheKey struct {
	kind      kube.ResourceKind
	namespace string
}

// cachedRowsFor returns rowCache's snapshot for kind/namespace, if any —
// used by resetAndLoad to seed the 15a "cached rows dimmed" loading view.
func (m Model) cachedRowsFor(kind kube.ResourceKind, namespace string) ([]resources.Row, bool) {
	rows, ok := m.rowCache[browseCacheKey{kind, namespace}]
	return rows, ok
}

// cacheCurrentRows snapshots m.rows into rowCache under the current
// kind/namespace, called once a load succeeds (applyRowsLoaded) — a copy,
// since m.rows is mutated in place by later sorts/marks.
func (m *Model) cacheCurrentRows() {
	if m.rowCache == nil {
		m.rowCache = make(map[browseCacheKey][]resources.Row)
	}
	cp := make([]resources.Row, len(m.rows))
	copy(cp, m.rows)
	m.rowCache[browseCacheKey{m.kind, m.namespace}] = cp
}

// resetAndLoad puts the model back into the loading state for whatever
// kind/namespace switchKind/switchNamespace just set, bumping both epochs
// so any in-flight reload/metrics reply for the previous kind/namespace is
// ignored, and re-issues load() (+ metrics polling for Pods).
func (m *Model) resetAndLoad() tea.Cmd {
	m.clearOrigin()
	m.state = tui.TaskStateLoading
	m.feedback = "Loading " + m.desc.Display + "..."
	m.loadStartedAt = time.Now()
	m.rows = nil
	m.pods = nil
	m.helmReleases = nil
	m.visible = nil
	m.expandedGroups = nil
	m.display = nil
	m.selected, m.offset = 0, 0
	m.filterActive = false
	m.setFilter("")
	m.nodeCount = 0
	m.podMetrics = nil
	m.nodeMetrics = nil
	m.nodeCapacity = nil
	m.podCountByNode = nil
	m.clusterPodTotal = 0
	// 20a: "marks are per-view and drop on kind/namespace switch."
	m.marks = nil
	m.pendingBulkDelete = nil
	m.reloadEpoch++
	m.metricsEpoch++

	// 15a: "revisiting a kind seen this session: cached rows dimmed instead
	// of skeletons" — seed the loading view from rowCache if this exact
	// kind+namespace was ever successfully loaded before; the fresh load
	// kicked off below still runs and replaces it once it lands
	// (applyRowsLoaded clears cachedView).
	m.cachedView = false
	if cached, ok := m.cachedRowsFor(m.kind, m.namespace); ok && len(cached) > 0 {
		m.rows = cached
		m.cachedView = true
		m.recomputeVisible()
	}

	if m.lister == nil {
		m.state = tui.TaskStateError
		m.feedback = "no cluster connection"
		return nil
	}
	cmds := []tea.Cmd{m.load(), components.SpinnerTick()}
	if m.pollsMetrics() {
		cmds = append(cmds, m.loadMetricsCmd(m.metricsEpoch), m.scheduleMetricsTick(m.metricsEpoch))
	}
	return tea.Batch(cmds...)
}

// switchContext applies a completed 7a cluster rebuild (tui.SwitchContextMsg
// with Err == nil, the only case update.go forwards it for): unlike
// switchKind/switchNamespace, kind/namespace are set unconditionally —
// even an unchanged kind/namespace string needs a fresh load, since the
// underlying cluster identity changed under it. msg.Filter is the target
// context's own remembered filter (state.PerContext, restored by
// switchContextCmd) — docs/design README.md §7a: "each context remembers
// its own namespace + kind + filter; switching restores them" — not the
// outgoing context's filter, so it replaces rather than preserves.
func (m *Model) switchContext(msg tui.SwitchContextMsg) tea.Cmd {
	if m.session == nil {
		return nil
	}
	desc, ok := m.session.Registry.Descriptor(msg.Kind)
	if !ok {
		return nil
	}
	m.kind = msg.Kind
	m.desc = desc
	m.namespace = msg.Namespace
	cmd := m.resetAndLoad()
	m.filterActive = msg.Filter != ""
	m.setFilter(msg.Filter)
	return cmd
}

// podsByName re-fetches the raw Pod objects for the 'l' logs verb: Row only
// carries the display Cells, not the container names StreamPodLogs needs
// per container (kube.PodFromObject's fuller projection). Best-effort — a
// failure here still leaves the table itself populated from resources.List.
func podsByName(ctx context.Context, lister resources.RawLister, namespace string) map[string]kube.Pod {
	objs, err := lister.ListRaw(ctx, kube.KindPod, namespace)
	if err != nil {
		return nil
	}
	out := make(map[string]kube.Pod, len(objs))
	for _, obj := range objs {
		if p, ok := obj.(*corev1.Pod); ok {
			out[p.Name] = kube.PodFromObject(p)
		}
	}
	return out
}

// helmReleasesByName re-fetches the full decoded kube.HelmRelease per row:
// Row only carries the display Cells, not the chart/values/revision fields
// 'v'/'h'/'R' need (helm.go's openReleaseValues/openReleaseHistory/
// beginRollback).
func helmReleasesByName(ctx context.Context, lister resources.RawLister, namespace string) map[string]kube.HelmRelease {
	objs, err := lister.ListRaw(ctx, kube.KindHelmRelease, namespace)
	if err != nil {
		return nil
	}
	out := make(map[string]kube.HelmRelease, len(objs))
	for _, obj := range objs {
		if ho, ok := obj.(*kube.HelmReleaseObject); ok {
			out[ho.Release.Name] = ho.Release
		}
	}
	return out
}
