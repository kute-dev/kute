// Package nodedetail is 11b (docs/design/README.md §11b): a node's
// CONDITIONS/ALLOCATED-ALLOCATABLE/TAINTS facts panel over the node's own
// pods table (sorted unhealthy-first then name, the same order 2a's own
// Pods list uses). Reached from tasks/browse's Nodes list on 'enter'.
//
// Pod-row 'enter' opens the same log-stream screen browse's 'l' verb does
// (tasks/poddetail doesn't exist yet — Phase 5) rather than a stub; see the
// package doc in mvp-tasks.md's Phase 9 exit notes for the scope call.
package nodedetail

import (
	"context"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/components"
)

// MetricsReader is the live pod-usage seam nodedetail needs for the bottom
// pane's MEM/CPU columns — same shape as browse.MetricsReader, duplicated
// per the repo's package-local-seam convention (CLAUDE.md: "define the
// interface you need in the task package").
type MetricsReader interface {
	PodMetricsByNamespace(ctx context.Context, namespace string) (map[string]kube.PodMetrics, error)
}

// OpenPodFunc pushes a view for the named pod — nodedetail hands it the same
// kube.Pod/width/height shape browse.OpenLogsFunc does, so app.go can wire
// both from the same closure.
type OpenPodFunc func(pod kube.Pod, width, height int) (tea.Model, tea.Cmd)

// OpenLogsFunc pushes the log-stream screen for pod — same shape as
// browse.OpenLogsFunc, so app.go can wire both from the same closure.
type OpenLogsFunc func(pod kube.Pod, width, height int) (tea.Model, tea.Cmd)

// OpenExecFunc pushes tasks/execpicker (10a) for a pod row with more than one
// container — same shape as browse.OpenExecFunc, duplicated per the repo's
// package-local-seam convention. A single container execs immediately via
// kube.ExecSpec without pushing a task.
type OpenExecFunc func(namespace, name string, containers []kube.ContainerInfo, width, height int) (tea.Model, tea.Cmd)

// OpenYAMLFunc pushes tasks/yamlview (8a) for the named object — same shape
// as browse.OpenYAMLFunc/poddetail.OpenYAMLFunc.
type OpenYAMLFunc func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd)

// OpenEventsFunc pushes tasks/events (9b) object-scoped for the loaded node
// — same shape as poddetail.OpenEventsFunc.
type OpenEventsFunc func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd)

// OpenTimelineFunc pushes tasks/timeline (16b) object-scoped for the loaded
// node (docs/design README.md §16b: "object-scoped from detail") — same
// shape as poddetail.OpenTimelineFunc.
type OpenTimelineFunc func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd)

// OpenForwardFunc pushes tasks/forwardpicker (13a) for a selected pod row —
// same shape as browse.OpenForwardFunc. The spec lists 'f' alongside 'x'/'y'
// as available "on any object row" (docs/design README.md §304, §308);
// browse already wires it for Pod rows, this closes the gap on a node's own
// pod rows.
type OpenForwardFunc func(target kube.ForwardTarget, width, height int) (tea.Model, tea.Cmd)

// Config are nodedetail's dependencies, per repo convention (package-local
// Config struct, interface-typed fields, New fills zero values).
type Config struct {
	Session      *tui.Session
	Lister       resources.RawLister
	Metrics      MetricsReader
	Mutator      kube.Mutator
	OpenPod      OpenPodFunc
	OpenLogs     OpenLogsFunc
	OpenExec     OpenExecFunc
	OpenYAML     OpenYAMLFunc
	OpenEvents   OpenEventsFunc
	OpenTimeline OpenTimelineFunc
	OpenForward  OpenForwardFunc
	NodeName     string
	LoadTimeout  time.Duration
}

// allocation is one CPU/MEM/Pods triple — used for the node/allocatable
// capacity and the node's pods' summed requests.
type allocation struct {
	cpuMilli, memBytes, pods int64
}

// nodePodRow is one bottom-pane row: pod carries what the row's actions
// (openPod/openLogs/openExec/openForward) key off; row is the same
// resources.Row the Pod descriptor's Project func produces for browse's own
// Pods list (Name/Namespace/Ready/Status/Restarts/CPU/MEM-placeholder/Node/
// Age cells, Status/Glyph/GlyphClass) — reusing it is what gives this
// screen's pods table the same kubectl-style status fidelity (Init:,
// CrashLoopBackOff, OOMKilled, Terminating…) browse's table already has.
type nodePodRow struct {
	pod kube.Pod
	row resources.Row
}

type Model struct {
	width, height int

	session      *tui.Session
	lister       resources.RawLister
	metrics      MetricsReader
	mutator      kube.Mutator
	actions      actions.Controller
	openPod      OpenPodFunc
	openLogs     OpenLogsFunc
	openExec     OpenExecFunc
	openYAML     OpenYAMLFunc
	openEvents   OpenEventsFunc
	openTimeline OpenTimelineFunc
	openForward  OpenForwardFunc
	timeout      time.Duration

	nodeName string

	node        *corev1.Node
	allocated   allocation
	allocatable allocation
	allPods     []nodePodRow // every pod load() found on this node, before the filter
	pods        []nodePodRow // allPods after filterQuery — what's selectable/rendered
	selected    int
	offset      int

	filterActive bool
	filterInput  textinput.Model

	// reloadEpoch guards a debounced reload-on-still-syncing retry (see
	// CacheSyncChecker/scheduleReload in load.go) against a stale reply
	// arriving after a newer one's already in flight — mirrors browse's own
	// reloadEpoch.
	reloadEpoch int

	// conn is the last kube.ConnStateMsg forwarded by the root shell — the
	// header badge's real connection state (never a hardcoded "connected").
	conn kube.ConnState
	// now is the wall-clock time as of the last SpinnerTickMsg/ConnStateMsg —
	// the loading header's "· 0.4s" counting timer is computed from this
	// rather than reading the clock in Render (render must stay pure:
	// f(model, theme, size)), mirroring browse's own now field.
	now time.Time

	state    tui.TaskState
	feedback string
	spinner  spinner.Model
	// loadStartedAt is when this screen's one-shot load() began — see now's
	// doc comment (docs/design README.md §15a's loading-state pattern,
	// applied here the same way browse.Model.loadStartedAt is).
	loadStartedAt time.Time

	// execFeedback carries a non-zero kubectl-exec exit's message (docs/design
	// README.md §10a: "feedback line on non-zero exit") — per-call-site
	// transient state, same as browse/poddetail's. Also carries kubectl
	// edit's exit message (editResultMsg) — same channel, same reasoning.
	execFeedback string
	// pendingEdit is non-nil while 'E' edit's PROD-only y/N line is showing
	// (verbs.TierForEdit) — mirrors browse.Model's own pendingEdit field.
	pendingEdit *editTarget
}

// loadedMsg carries one load()'s result: the node itself plus its pods
// (already sorted unhealthy-first then name) and their summed requests.
type loadedMsg struct {
	node        *corev1.Node
	allocated   allocation
	allocatable allocation
	pods        []nodePodRow
	err         error
}

func New(cfg Config) Model {
	if cfg.LoadTimeout == 0 {
		cfg.LoadTimeout = 10 * time.Second
	}
	state := tui.TaskStateLoading
	feedback := "Loading " + cfg.NodeName + "..."
	if cfg.Lister == nil {
		state = tui.TaskStateError
		feedback = "no cluster connection"
	}
	return Model{
		width:         tui.DefaultWidth,
		height:        tui.DefaultHeight,
		session:       cfg.Session,
		lister:        cfg.Lister,
		metrics:       cfg.Metrics,
		mutator:       cfg.Mutator,
		actions:       actions.New(cfg.Mutator),
		openPod:       cfg.OpenPod,
		openLogs:      cfg.OpenLogs,
		openExec:      cfg.OpenExec,
		openYAML:      cfg.OpenYAML,
		openEvents:    cfg.OpenEvents,
		openTimeline:  cfg.OpenTimeline,
		openForward:   cfg.OpenForward,
		timeout:       cfg.LoadTimeout,
		nodeName:      cfg.NodeName,
		state:         state,
		feedback:      feedback,
		now:           time.Now(),
		loadStartedAt: time.Now(),
		spinner:       components.NewSpinner(),
	}
}

func (m Model) Init() tea.Cmd {
	if m.lister == nil {
		return nil
	}
	return tea.Batch(m.load(), m.spinner.Tick)
}

func (m *Model) SetSize(width, height int) {
	size := tui.NormalizeSize(width, height)
	m.width, m.height = size.Width, size.Height
}

// CacheSyncChecker is optionally implemented by a Lister whose cache
// populates asynchronously (*kube.Cluster's informers, right after launch or
// mid SwitchContext) — duplicated from browse's own CacheSyncChecker per the
// repo's package-local-seam convention, since resources.RawLister itself
// carries no such method. ListRaw reads the cache regardless of sync state,
// so a load() landing right after this screen opens can see a truthful-
// looking empty pod list before the first real objects have arrived;
// listerSynced tells that apart from the node genuinely having no pods.
type CacheSyncChecker interface {
	Synced() bool
}

// listerSynced reports whether m.lister's cache has finished its initial
// sync — true for any lister that doesn't opt into CacheSyncChecker (fakes,
// test doubles), so this only changes behavior for *kube.Cluster. Mirrors
// browse.Model.listerSynced.
func (m Model) listerSynced() bool {
	sc, ok := m.lister.(CacheSyncChecker)
	return !ok || sc.Synced()
}

func (m Model) selectedPod() (nodePodRow, bool) {
	if m.selected < 0 || m.selected >= len(m.pods) {
		return nodePodRow{}, false
	}
	return m.pods[m.selected], true
}
