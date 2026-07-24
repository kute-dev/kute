// Package podlogs is 5b (docs/design/README.md §5b): the streaming
// log-view screen, reached from tasks/browse's Pods list and tasks/
// poddetail on 'l'. Restyled onto Chrome v2 in Phase 6 (mvp-tasks.md) —
// stream machinery (this file, stream.go) carries over from the pre-
// redesign screen; view.go/keys.go are new.
package podlogs

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

const DefaultMaxEntries = 5000
const DefaultTailLines int64 = 200

// SelectedPod is the pod identity + container list podlogs needs — a
// trimmed projection of kube.Pod (no CPU/MEM/Age/Node: 5b's toolbar has no
// slot for them, and the pod that opened this screen — browse or
// poddetail — already showed that metadata).
type SelectedPod struct {
	Context    string
	Namespace  string
	Name       string
	Containers []string
	Restarts   int32
}

// sinceOption is one entry in the 's' since-window cycle.
type sinceOption struct {
	label   string
	seconds int64
}

// sinceOptions is the 's' cycle (docs/design README.md §5b: "since 15m ...
// s changes since-window"); index 1 ("15m") is the toolbar's default.
var sinceOptions = []sinceOption{
	{"5m", 300},
	{"15m", 900},
	{"1h", 3600},
	{"6h", 21600},
	{"all", 0},
}

const defaultSinceIndex = 1

// LogViewState is the display-only presentation state — none of these
// fields require a stream restart to change (Timestamps/Wrap are parsed/
// wrapped at render time; only the container and since-window selections,
// which change what's actually requested from the API, do).
type LogViewState struct {
	AutoScroll       bool // "follow" (docs/design README.md §5b)
	Wrap             bool
	Timestamps       bool
	VerticalOffset   int
	HorizontalOffset int
	Width            int
	Height           int
}

// LogEntry is one rendered line: either a real streamed log line
// (Container/Timestamp/Message/Severity) or a synthesized restart-boundary
// marker (Boundary set, Message carries "container restarted · restart N").
type LogEntry struct {
	Container string
	Timestamp string
	Message   string
	Severity  string // "", SeverityInfo, SeverityWarn, SeverityErr
	Boundary  bool
}

type LogBuffer struct {
	Entries      []LogEntry
	MaxEntries   int
	DroppedCount int
}

type StreamState string

const (
	StreamIdle         StreamState = "idle"
	StreamLoading      StreamState = "loading"
	StreamStreaming    StreamState = "streaming"
	StreamReconnecting StreamState = "reconnecting"
	StreamEmpty        StreamState = "empty"
	StreamError        StreamState = "error"
	StreamClosed       StreamState = "closed"
)

// Config are podlogs' dependencies, per repo convention (package-local
// Config struct, interface-typed fields, New fills zero values).
type Config struct {
	Session *tui.Session
	// Lister is optional — used only to refresh the pod's live restart
	// count when synthesizing a reconnect boundary entry. A nil Lister
	// falls back to the Restarts count captured when the screen opened.
	Lister     resources.RawLister
	Pod        SelectedPod
	Streamer   kube.PodLogStreamer
	MaxEntries int
	TailLines  int64
}

type Model struct {
	width, height int

	session *tui.Session
	lister  resources.RawLister

	pod          SelectedPod
	containerIdx int
	sinceIdx     int

	view       LogViewState
	buffer     LogBuffer
	stream     StreamState
	lastError  string
	permDenied bool
	feedback   string
	streamer   kube.PodLogStreamer
	tailLines  int64

	streamCancel context.CancelFunc
	streamCh     chan tea.Msg
	streamID     int

	filterActive bool
	filterInput  textinput.Model

	rateGen        int
	linesSinceTick int
	lastRate       int

	spinner components.Spinner
}

func New(cfg Config) Model {
	if cfg.Pod.Namespace == "" {
		cfg.Pod.Namespace = "default"
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = DefaultMaxEntries
	}
	if cfg.TailLines <= 0 {
		cfg.TailLines = DefaultTailLines
	}

	return Model{
		width:   tui.DefaultWidth,
		height:  tui.DefaultHeight,
		session: cfg.Session,
		lister:  cfg.Lister,
		pod:     cfg.Pod,
		view: LogViewState{
			AutoScroll: true,
			Wrap:       true,
			Width:      tui.DefaultWidth,
			Height:     tui.DefaultHeight,
		},
		buffer:    LogBuffer{MaxEntries: cfg.MaxEntries},
		stream:    StreamIdle,
		feedback:  "Loading logs...",
		streamer:  cfg.Streamer,
		tailLines: cfg.TailLines,
		sinceIdx:  defaultSinceIndex,
	}
}

// FromPod builds podlogs for pod, opened from browse/poddetail — the shape
// every OpenLogsFunc caller in internal/app wires against.
func FromPod(session *tui.Session, lister resources.RawLister, pod kube.Pod, streamer kube.PodLogStreamer) Model {
	return New(Config{
		Session: session,
		Lister:  lister,
		Pod: SelectedPod{
			Context:    pod.Context,
			Namespace:  pod.Namespace,
			Name:       pod.Name,
			Containers: pod.Containers,
			Restarts:   pod.Restarts,
		},
		Streamer: streamer,
	})
}

func (m Model) Init() tea.Cmd { return nil }

// Start begins streaming — called once by the OpenLogsFunc caller
// alongside pushing the screen (mirrors poddetail/nodedetail's Init()
// pattern, kept as an explicit method since browse already calls it that
// way and changing the call site isn't otherwise needed).
func (m *Model) Start() tea.Cmd { return m.restartStream(StreamLoading) }

func (m Model) Theme() tui.Theme {
	if m.session != nil {
		return m.session.Theme
	}
	return tui.Dark()
}

func (m *Model) SetSize(width, height int) {
	size := tui.NormalizeSize(width, height)
	m.width, m.height = size.Width, size.Height
	m.view.Width, m.view.Height = size.Width, size.Height
	m.clampOffsets()
}

func (b *LogBuffer) Append(entry LogEntry) {
	if b.MaxEntries <= 0 {
		b.MaxEntries = DefaultMaxEntries
	}
	b.Entries = append(b.Entries, entry)
	if over := len(b.Entries) - b.MaxEntries; over > 0 {
		b.Entries = append([]LogEntry(nil), b.Entries[over:]...)
		b.DroppedCount += over
	}
}

func (m *Model) appendEntry(entry LogEntry) {
	m.buffer.Append(entry)
	m.linesSinceTick++
	if m.stream != StreamError {
		m.stream = StreamStreaming
	}
	m.feedback = ""
	if m.view.AutoScroll {
		m.view.VerticalOffset = m.maxVerticalOffset()
	}
	m.clampOffsets()
}

// filteredEntries is the buffer narrowed by the live '/' filter (a plain
// case-insensitive substring match on the message — simpler and cheaper
// than the table's fuzzy grammar, and a better fit for searching arbitrary
// log text than fuzzy-matching short names). Boundary markers always pass,
// so a restart's context isn't hidden by an unrelated filter.
func (m Model) filteredEntries() []LogEntry {
	if m.filterInput.Value() == "" {
		return m.buffer.Entries
	}
	query := strings.ToLower(m.filterInput.Value())
	out := make([]LogEntry, 0, len(m.buffer.Entries))
	for _, e := range m.buffer.Entries {
		if e.Boundary || strings.Contains(strings.ToLower(e.Message), query) {
			out = append(out, e)
		}
	}
	return out
}

func (m *Model) clampOffsets() {
	if m.view.VerticalOffset < 0 {
		m.view.VerticalOffset = 0
	}
	if maxOff := m.maxVerticalOffset(); m.view.VerticalOffset > maxOff {
		m.view.VerticalOffset = maxOff
	}
	if m.view.HorizontalOffset < 0 || m.view.Wrap {
		m.view.HorizontalOffset = 0
	}
}

func (m Model) maxVerticalOffset() int {
	visible := m.entryViewportHeight()
	total := len(m.filteredEntries())
	if total <= visible {
		return 0
	}
	return total - visible
}

func (m Model) entryViewportHeight() int {
	height := m.viewportHeight() - 1 // bottom status line
	if m.buffer.DroppedCount > 0 {
		height--
	}
	if height < 1 {
		return 1
	}
	return height
}

// viewportHeight is the body height Frame budgets, computed the same way
// every other Chrome v2 screen does (tui.FrameBodyHeight against this
// screen's strip-line count) — stripLineCount rather than len(Strips(...))
// deliberately, since Strips renders the toolbar's severity-in-view counts
// from the very entry viewport this method computes (a real cycle: Strips
// -> visibleEntries -> entryViewportHeight -> viewportHeight -> Strips).
func (m Model) viewportHeight() int {
	return tui.FrameBodyHeight(m.height, m.stripLineCount())
}

// stripLineCount is Strips' line count without rendering it — see
// viewportHeight's doc comment for why that split matters.
func (m Model) stripLineCount() int {
	if m.taskState() != tui.TaskStateReady {
		return 0
	}
	n := 1
	if m.filterActive {
		n++
	}
	return n
}

func (m Model) activeContainer() (string, bool) {
	if len(m.pod.Containers) == 0 {
		return "", false
	}
	idx := m.containerIdx
	if idx < 0 || idx >= len(m.pod.Containers) {
		idx = 0
	}
	return m.pod.Containers[idx], true
}

// nextContainer is what 'tab' would switch to — used by both the toolbar's
// "(tab: metrics-sidecar)" hint and cycleContainer's mutation, so the two
// never drift.
func (m Model) nextContainerIndex() int {
	if len(m.pod.Containers) == 0 {
		return 0
	}
	return (m.containerIdx + 1) % len(m.pod.Containers)
}

func (m *Model) cycleContainer() {
	if len(m.pod.Containers) <= 1 {
		return
	}
	m.containerIdx = m.nextContainerIndex()
}

func (m *Model) cycleSince() {
	m.sinceIdx = (m.sinceIdx + 1) % len(sinceOptions)
}

func (m Model) sinceSeconds() int64 { return sinceOptions[m.sinceIdx].seconds }
func (m Model) sinceLabel() string  { return sinceOptions[m.sinceIdx].label }

func (m *Model) cancelStream() {
	if m.streamCancel != nil {
		m.streamCancel()
		m.streamCancel = nil
	}
}

func (m Model) scope() string {
	container, _ := m.activeContainer()
	return strings.TrimSpace(m.pod.Context + "/" + m.pod.Namespace + "/" + m.pod.Name + "/" + container)
}

func (m Model) taskState() tui.TaskState {
	switch m.stream {
	case StreamEmpty:
		return tui.TaskStateEmpty
	case StreamError:
		if kube.IsPermissionError(fmt.Errorf("%s", m.lastError)) {
			return tui.TaskStatePermissionDenied
		}
		return tui.TaskStateError
	case StreamLoading, StreamReconnecting, StreamIdle:
		return tui.TaskStateLoading
	default:
		return tui.TaskStateReady
	}
}
