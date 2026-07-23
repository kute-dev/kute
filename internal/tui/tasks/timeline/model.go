// Package timeline is 16a/16b (docs/design/README.md §16a/§16b): one merged
// clock — Events, container restarts, and Deployment rollout revisions —
// newest first, answering "what changed" during an incident. Reached
// namespace-scoped from tasks/browse's 't' (16a) and object-scoped from
// tasks/poddetail and tasks/nodedetail's 't' (16b), the same dual-mode shape
// tasks/events already establishes (ObjectKind/ObjectName empty = namespace
// scope).
//
// 16b additionally grows a revision rail — "deployment revisions as a
// vertical rail with the current one highlighted" — when the scoped object
// resolves to an owning Deployment (a Pod's ReplicaSet owner, or the
// Deployment itself); tasks/helmhistory (18a's 'h') later reused this same
// rail idiom for Helm release history.
package timeline

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

// EventsReader is the seam timeline needs for its Events source — the same
// interface tasks/events declares (ObjectEvents/NamespaceEvents), satisfied
// by *kube.Cluster and *fake.Cluster already.
type EventsReader interface {
	NamespaceEvents(ctx context.Context, namespace string) ([]kube.Event, error)
	ObjectEvents(ctx context.Context, namespace string, kind kube.ResourceKind, name string) ([]kube.Event, error)
}

// windowSteps are the 't' time-window cycle's stops. 16a's default is "last
// 30m" (docs/design README.md §16a's breadcrumb tag).
var windowSteps = []time.Duration{30 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour, 0}

// OpenEventsFunc pushes tasks/events (9b) for the 'e' global verb — object-
// scoped (kind/name non-empty) when timeline itself is 16b object-scoped,
// namespace-scoped (kind/name empty) when it's 16a — same signature as
// poddetail/nodedetail's own OpenEventsFunc so app.go's openObjectEventsFunc
// closure can be reused directly for the 16b wiring.
type OpenEventsFunc func(kind kube.ResourceKind, namespace, name string, width, height int) (tea.Model, tea.Cmd)

// Config are timeline's dependencies, per repo convention (package-local
// Config struct, interface-typed fields, New fills zero values).
// ObjectKind/ObjectName non-empty switches the screen into 16b's
// object-scoped mode; empty means 16a's namespace-scoped mode (Namespace ==
// "" mirrors 6b's all-namespaces triage, same rule tasks/events already
// follows).
type Config struct {
	Session     *tui.Session
	Events      EventsReader
	Lister      resources.RawLister
	Namespace   string
	ObjectKind  kube.ResourceKind
	ObjectName  string
	OpenEvents  OpenEventsFunc
	LoadTimeout time.Duration
}

type Model struct {
	width, height int

	session    *tui.Session
	events     EventsReader
	lister     resources.RawLister
	openEvents OpenEventsFunc
	timeout    time.Duration

	namespace  string
	objectKind kube.ResourceKind
	objectName string

	entries []kube.TimelineEntry // every merged entry, unwindowed
	rows    []kube.TimelineEntry // entries after the window/filter — what's walked/rendered
	// filterBaselineRows is recomputeVisible's own window-only tally (no
	// filterQuery applied) — lets the strip say how many the query itself
	// hid, same reasoning as tasks/events' filterBaselineGroups.
	filterBaselineRows int
	selected           int

	// rail is 16b's revision rail: the resolved owning Deployment's
	// TimelineRollout entries, newest-first, index 0 == current. Empty in
	// 16a (namespace-scoped) and in 16b whenever the object doesn't resolve
	// to a Deployment (e.g. a Node).
	rail           []kube.TimelineEntry
	railDeployment string

	window       time.Duration
	filterActive bool
	filterQuery  string

	// conn is the last kube.ConnStateMsg forwarded by the root shell — the
	// header badge's real connection state (never a hardcoded "connected").
	conn kube.ConnState

	fetchedAt time.Time
	state     tui.TaskState
	feedback  string
	spinner   components.Spinner
}

// loadedMsg carries one load()'s result.
type loadedMsg struct {
	entries        []kube.TimelineEntry
	rail           []kube.TimelineEntry
	railDeployment string
	err            error
}

func New(cfg Config) Model {
	if cfg.LoadTimeout == 0 {
		cfg.LoadTimeout = 10 * time.Second
	}
	state := tui.TaskStateLoading
	feedback := "Loading timeline..."
	if cfg.Events == nil {
		state = tui.TaskStateError
		feedback = "no cluster connection"
	}
	return Model{
		width:      tui.DefaultWidth,
		height:     tui.DefaultHeight,
		session:    cfg.Session,
		events:     cfg.Events,
		lister:     cfg.Lister,
		openEvents: cfg.OpenEvents,
		timeout:    cfg.LoadTimeout,
		namespace:  cfg.Namespace,
		objectKind: cfg.ObjectKind,
		objectName: cfg.ObjectName,
		window:     windowSteps[0],
		state:      state,
		feedback:   feedback,
	}
}

func (m Model) Init() tea.Cmd {
	if m.events == nil {
		return nil
	}
	return tea.Batch(m.load(), components.SpinnerTick())
}

func (m *Model) SetSize(width, height int) {
	size := tui.NormalizeSize(width, height)
	m.width, m.height = size.Width, size.Height
}

// objectScoped reports whether this is 16b (an object-scoped feed) rather
// than 16a (namespace-scoped).
func (m Model) objectScoped() bool {
	return m.objectKind != "" && m.objectName != ""
}

func (m Model) selectedRow() (kube.TimelineEntry, bool) {
	if m.selected < 0 || m.selected >= len(m.rows) {
		return kube.TimelineEntry{}, false
	}
	return m.rows[m.selected], true
}

// openSelectedObject is 16a/16b's "↵ goes to the object": pop back to
// whatever pushed this screen and dispatch a jump to the entry's involved
// object — the same tea.Sequence(BackMsg, GotoResourceMsg) pair tasks/events'
// own openSelectedObject already established.
func (m Model) openSelectedObject() (tea.Cmd, bool) {
	row, ok := m.selectedRow()
	if !ok {
		return nil, false
	}
	kind, name := splitObject(row.Object)
	if kind == "" || name == "" {
		return nil, false
	}
	ns := row.Namespace
	return tea.Sequence(
		func() tea.Msg { return tui.BackMsg{} },
		func() tea.Msg { return tui.GotoResourceMsg{Kind: kind, Namespace: ns, Name: name} },
	), true
}

// openSelectedEvents is the 'e' global verb: pushes 9b scoped exactly like
// this timeline itself is scoped — object-scoped in 16b, namespace-scoped
// in 16a (docs/design README.md: "e opens events (namespace-scoped from a
// list view; object-scoped from a detail view)").
func (m Model) openSelectedEvents() (tea.Model, tea.Cmd, bool) {
	if m.openEvents == nil {
		return nil, nil, false
	}
	task, cmd := m.openEvents(m.objectKind, m.namespace, m.objectName, m.width, m.height)
	return task, cmd, task != nil
}
