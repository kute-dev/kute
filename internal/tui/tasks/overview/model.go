// Package overview is 19a (docs/design/README.md §19a): the cluster
// overview — cluster-scoped (namespace drops from the breadcrumb, like
// Nodes/CRDs/WhoCan) and reached only via the goto corpus (`g "ov"`,
// tui/goto.go's gotoOverviewItem). "Not the start screen" — tasks/browse's
// pods table remains the resting state; this is a routing layer, not a
// dashboard: CAPACITY (cluster cpu/mem/pods bars, no selection of its own)
// and NODES (pressure/cordoned first, "+N ready" collapse) sit in the left
// column; TROUBLE (cluster-wide unhealthy-first pod aggregation) and RECENT
// CHANGES (the timeline's rollout feed, cluster-wide, 30m) sit in the
// right. `↹` cycles focus between the three selectable panels (NODES/
// TROUBLE/CHANGES — CAPACITY is facts only); every focused row's ↵ lands on
// an existing screen (11b node detail, or back-and-jump to the object, the
// same tea.Sequence(BackMsg, GotoResourceMsg) pair tasks/timeline's own
// openSelectedObject already establishes for the identical "pushed on top
// of browse" shape). `t`/`e` escape to the full cluster-wide timeline/
// events screens (namespace "" — the same all-namespaces convention
// tasks/timeline and tasks/events already use).
package overview

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

// changesWindow is 19a's fixed "last 30m" RECENT CHANGES window (docs/design
// README.md §19a). Unlike tasks/timeline's own `t` window cycle (16a's
// windowSteps), this screen shows one fixed window — `t` escapes to the full
// timeline screen for anything wider.
const changesWindow = 30 * time.Minute

// NodeMetricsReader is the live node-usage seam CAPACITY needs for its cpu/
// mem bars — the same interface tasks/browse declares for 11a's own bars,
// satisfied by *kube.Cluster and *kube/fake.Cluster already.
type NodeMetricsReader interface {
	NodeMetrics(ctx context.Context) (map[string]kube.NodeMetric, error)
}

// OpenNodeDetailFunc pushes tasks/nodedetail (11b) for a NODES row — same
// shape as browse's own OpenNodeDetailFunc.
type OpenNodeDetailFunc func(nodeName string, width, height int) (tea.Model, tea.Cmd)

// OpenTimelineFunc pushes tasks/timeline (16a), cluster-wide (namespace "").
type OpenTimelineFunc func(namespace string, width, height int) (tea.Model, tea.Cmd)

// OpenEventsFunc pushes tasks/events (9b), cluster-wide (namespace "").
type OpenEventsFunc func(namespace string, width, height int) (tea.Model, tea.Cmd)

// Config are overview's dependencies, per repo convention (package-local
// Config struct, interface-typed fields, New fills zero values).
type Config struct {
	Session        *tui.Session
	Lister         resources.RawLister
	NodeMetrics    NodeMetricsReader
	OpenNodeDetail OpenNodeDetailFunc
	OpenTimeline   OpenTimelineFunc
	OpenEvents     OpenEventsFunc
	LoadTimeout    time.Duration
}

// panel identifies one of 19a's three selectable panels — CAPACITY carries
// no selection of its own, so it isn't one of these.
type panel int

const (
	panelNodes panel = iota
	panelTrouble
	panelChanges
)

type Model struct {
	width, height int

	session        *tui.Session
	lister         resources.RawLister
	nodeMetrics    NodeMetricsReader
	openNodeDetail OpenNodeDetailFunc
	openTimeline   OpenTimelineFunc
	openEvents     OpenEventsFunc
	timeout        time.Duration

	focus panel

	version   string
	nodeCount int
	podCount  int
	nsCount   int

	metricsAvailable          bool
	capCPUUsed, capCPUTotal   int64
	capMemUsed, capMemTotal   int64
	capPodsUsed, capPodsTotal int64

	// nodeTrouble/podTrouble are the unhealthy (Warn/Fail, or cordoned for
	// nodes) rows only — the healthy remainder is just a count, per the
	// "+N ready"/"nothing unhealthy" collapse idiom.
	nodeTrouble []resources.Row
	nodeHealthy int
	nodesSel    int

	podTrouble []resources.Row
	podHealthy int
	troubleSel int

	changes    []kube.TimelineEntry
	changesSel int

	// conn is the last kube.ConnStateMsg forwarded by the root shell — the
	// header badge's real connection state, same as every other screen.
	conn kube.ConnState

	reloadEpoch int
	state       tui.TaskState
	feedback    string
	spinner     components.Spinner
}

// loadedMsg carries one load()'s result.
type loadedMsg struct {
	epoch int
	data  loadedData
	err   error
}

// loadedData is every field load() computes in one pass — applied onto
// Model atomically by applyLoaded so a render never sees a half-updated
// screen.
type loadedData struct {
	version   string
	nodeCount int
	podCount  int
	nsCount   int

	metricsAvailable          bool
	capCPUUsed, capCPUTotal   int64
	capMemUsed, capMemTotal   int64
	capPodsUsed, capPodsTotal int64

	nodeTrouble []resources.Row
	nodeHealthy int

	podTrouble []resources.Row
	podHealthy int

	changes []kube.TimelineEntry
}

func New(cfg Config) Model {
	if cfg.LoadTimeout == 0 {
		cfg.LoadTimeout = 10 * time.Second
	}
	state := tui.TaskStateLoading
	feedback := "Loading cluster overview..."
	if cfg.Lister == nil {
		state = tui.TaskStateError
		feedback = "no cluster connection"
	}
	return Model{
		width:          tui.DefaultWidth,
		height:         tui.DefaultHeight,
		session:        cfg.Session,
		lister:         cfg.Lister,
		nodeMetrics:    cfg.NodeMetrics,
		openNodeDetail: cfg.OpenNodeDetail,
		openTimeline:   cfg.OpenTimeline,
		openEvents:     cfg.OpenEvents,
		timeout:        cfg.LoadTimeout,
		state:          state,
		feedback:       feedback,
	}
}

func (m Model) Init() tea.Cmd {
	if m.lister == nil {
		return nil
	}
	return tea.Batch(m.load(), components.SpinnerTick())
}

func (m *Model) SetSize(width, height int) {
	size := tui.NormalizeSize(width, height)
	m.width, m.height = size.Width, size.Height
}

func (m Model) selectedNode() (resources.Row, bool) {
	if m.nodesSel < 0 || m.nodesSel >= len(m.nodeTrouble) {
		return resources.Row{}, false
	}
	return m.nodeTrouble[m.nodesSel], true
}

func (m Model) selectedTrouble() (resources.Row, bool) {
	if m.troubleSel < 0 || m.troubleSel >= len(m.podTrouble) {
		return resources.Row{}, false
	}
	return m.podTrouble[m.troubleSel], true
}

func (m Model) selectedChange() (kube.TimelineEntry, bool) {
	if m.changesSel < 0 || m.changesSel >= len(m.changes) {
		return kube.TimelineEntry{}, false
	}
	return m.changes[m.changesSel], true
}
