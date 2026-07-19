// Package poddetail is 5a (docs/design/README.md §5a): a pod's full detail
// view — title/status/restarts, last-termination banner (promoted first
// when present), meta grid, CONTAINERS grid, CPU/MEM bars vs limits,
// EVENTS, and a LABELS/RELATED/TOLERATIONS sidebar. Reached from
// tasks/browse's Pods list on 'enter', and from tasks/nodedetail's pod rows
// (mvp-tasks.md Phase 9 exit notes: "swap it for a genuine poddetail push
// once Phase 5 lands").
//
// YAML view ('y', 8a) and the full type-the-name PROD confirm modal (8b)
// aren't built yet — see this package's callers in mvp-tasks.md's Phase 5
// exit notes for the scope calls made here.
package poddetail

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/components"
)

// MetricsReader is the live pod-usage seam poddetail needs for the CPU/MEM
// bars — same shape as browse.MetricsReader/nodedetail.MetricsReader,
// duplicated per the repo's package-local-seam convention.
type MetricsReader interface {
	PodMetricsByNamespace(ctx context.Context, namespace string) (map[string]kube.PodMetrics, error)
}

// EventsReader is the seam for the EVENTS grid — satisfied by *kube.Cluster
// and *fake.Cluster already (kube/events.go, kube/fake/fake.go).
type EventsReader interface {
	ObjectEvents(ctx context.Context, namespace string, kind kube.ResourceKind, name string) ([]kube.Event, error)
}

// OpenLogsFunc pushes the log-stream screen for pod — same shape as
// browse.OpenLogsFunc, so app.go can wire both from the same closure.
type OpenLogsFunc func(pod kube.Pod, width, height int) (tea.Model, tea.Cmd)

// OpenYAMLFunc pushes tasks/yamlview (8a) for the named object — same shape
// as browse.OpenYAMLFunc.
type OpenYAMLFunc func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd)

// OpenEventsFunc pushes tasks/events (9b) object-scoped for the loaded pod
// (docs/design README.md §9b: "object-scoped from detail, reusing
// ObjectEvents").
type OpenEventsFunc func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd)

// OpenTimelineFunc pushes tasks/timeline (16b) object-scoped for the loaded
// pod (docs/design README.md §16b: "object-scoped from detail").
type OpenTimelineFunc func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd)

// OpenExecFunc pushes tasks/execpicker (10a) for the loaded pod when it has
// more than one container — same shape as browse.OpenExecFunc, duplicated
// per the repo's package-local-seam convention.
type OpenExecFunc func(namespace, name string, containers []kube.ContainerInfo, width, height int) (tea.Model, tea.Cmd)

// OpenForwardFunc pushes tasks/forwardpicker (13a) for the loaded pod — same
// shape as browse.OpenForwardFunc. The spec lists 'f' alongside 'x'/'y' as
// available "on any object row" (docs/design README.md §304, §308); browse
// already wires it for Pod rows, this closes the gap on the pod's own
// detail screen.
type OpenForwardFunc func(target kube.ForwardTarget, width, height int) (tea.Model, tea.Cmd)

// Config are poddetail's dependencies, per repo convention (package-local
// Config struct, interface-typed fields, New fills zero values). Siblings/
// SiblingIndex are the ordered pod-name list + cursor browse hands over so
// j/k can move to the next/prev pod without leaving detail (docs/design
// README.md §5a).
type Config struct {
	Session      *tui.Session
	Lister       resources.RawLister
	Metrics      MetricsReader
	Events       EventsReader
	Mutator      kube.Mutator
	OpenLogs     OpenLogsFunc
	OpenYAML     OpenYAMLFunc
	OpenEvents   OpenEventsFunc
	OpenTimeline OpenTimelineFunc
	OpenExec     OpenExecFunc
	OpenForward  OpenForwardFunc
	Namespace    string
	Name         string
	Siblings     []string
	SiblingIndex int
	LoadTimeout  time.Duration
}

type Model struct {
	width, height int

	session      *tui.Session
	lister       resources.RawLister
	metrics      MetricsReader
	events       EventsReader
	mutator      kube.Mutator
	actions      actions.Controller
	openLogs     OpenLogsFunc
	openYAML     OpenYAMLFunc
	openEvents   OpenEventsFunc
	openTimeline OpenTimelineFunc
	openExec     OpenExecFunc
	openForward  OpenForwardFunc
	timeout      time.Duration
	// execFeedback carries a non-zero directly-run kubectl-exec exit's
	// message (single-container pods exec straight from poddetail without
	// pushing execpicker) — mirrors browse.Model's own execFeedback field.
	// Also carries kubectl edit's exit message (editResultMsg) — same
	// transient channel, same reasoning.
	execFeedback string
	// pendingEdit is non-nil while 'E' edit's PROD-only y/N line is showing
	// (verbs.TierForEdit) — mirrors browse.Model's own pendingEdit field.
	pendingEdit *editTarget

	namespace    string
	name         string
	siblings     []string
	siblingIndex int

	pod   kube.Pod
	found bool
	// gone is set once a load() reports the pod no longer exists (watch
	// delete) — Body() renders the "pod gone" banner and every key becomes
	// "go back" rather than the normal keymap.
	gone bool
	// controller is 5a's resolved CONTROLLER display text (loadedMsg's own
	// field doc comment explains the ReplicaSet→Deployment hop) — separate
	// from pod.Owner, which RELATED/alt+o still read as the pod's direct,
	// unresolved owner.
	controller string

	eventRows []kube.Event
	// eventsErr is the last events fetch's failure — the EVENTS grid shows
	// "events unavailable" instead of a misleading "no events" (a throttled
	// or timed-out call is not an empty result).
	eventsErr error

	// conn is the last kube.ConnStateMsg forwarded by the root shell — the
	// header badge's real connection state (mock 5a: "● connected · 12ms",
	// red "◌ disconnected" mid-outage).
	conn kube.ConnState

	// selectedContainer highlights a row in the CONTAINERS grid ('tab'
	// cycles it) — display-only in this pass, no exec/logs container
	// targeting wired yet.
	selectedContainer int

	state    tui.TaskState
	feedback string
	spinner  components.Spinner
}

// loadedMsg carries one load()'s result for m.name/m.namespace as of when it
// was issued — applyLoaded doesn't need a name guard the way browse's
// rowsLoadedMsg does, since a sibling move (moveSibling) updates m.name
// before re-issuing load(), and no other path changes it mid-flight.
type loadedMsg struct {
	pod    kube.Pod
	found  bool
	events []kube.Event
	// eventsErr is the best-effort events fetch's own failure — it never
	// fails the load (err stays nil), but the EVENTS grid distinguishes it
	// from a genuinely empty result.
	eventsErr error
	err       error
	// controller is 5a's CONTROLLER field display text — pod.Owner, except
	// a ReplicaSet owner resolves one hop further to its own owning
	// Deployment (docs/design README.md §5a: "deploy/nva-worker ↗"), since a
	// Deployment never appears as a pod's direct owner. Resolved here
	// (load()'s tea.Cmd) rather than in metaGrid, which must stay pure.
	controller string
}

func New(cfg Config) Model {
	if cfg.LoadTimeout == 0 {
		cfg.LoadTimeout = 10 * time.Second
	}
	state := tui.TaskStateLoading
	feedback := "Loading " + cfg.Name + "..."
	if cfg.Lister == nil {
		state = tui.TaskStateError
		feedback = "no cluster connection"
	}
	return Model{
		width:        tui.DefaultWidth,
		height:       tui.DefaultHeight,
		session:      cfg.Session,
		lister:       cfg.Lister,
		metrics:      cfg.Metrics,
		events:       cfg.Events,
		mutator:      cfg.Mutator,
		actions:      actions.New(cfg.Mutator),
		openLogs:     cfg.OpenLogs,
		openYAML:     cfg.OpenYAML,
		openEvents:   cfg.OpenEvents,
		openTimeline: cfg.OpenTimeline,
		openExec:     cfg.OpenExec,
		openForward:  cfg.OpenForward,
		timeout:      cfg.LoadTimeout,
		namespace:    cfg.Namespace,
		name:         cfg.Name,
		siblings:     cfg.Siblings,
		siblingIndex: cfg.SiblingIndex,
		state:        state,
		feedback:     feedback,
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
