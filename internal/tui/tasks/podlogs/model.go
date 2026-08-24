// Package podlogs is 5b (docs/design/README.md §5b): the streaming
// log-view screen, reached from tasks/browse's Pods list and tasks/
// poddetail on 'l'. Restyled onto Chrome v2 in Phase 6 (mvp-tasks.md) —
// stream machinery (this file, stream.go) carries over from the pre-
// redesign screen; view.go/keys.go are new.
package podlogs

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/spinner"
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
	StreamIdle      StreamState = "idle"
	StreamLoading   StreamState = "loading"
	StreamStreaming StreamState = "streaming"
	// StreamWaitingForContainer is the pre-connect state: the active
	// container hasn't started yet per the pod cache (checkContainerCmd's
	// containerWaitingMsg, stream.go), so no stream has been opened at all.
	// Distinct from StreamReconnecting, which is between two actual
	// connections against a container that has already run. Resolved by
	// the next kube.ResourceChangedMsg{Kind: KindPod} (update.go), not a
	// timer.
	StreamWaitingForContainer StreamState = "waiting-container"
	StreamReconnecting        StreamState = "reconnecting"
	StreamEmpty               StreamState = "empty"
	StreamError               StreamState = "error"
	StreamClosed              StreamState = "closed"
)

// Config are podlogs' dependencies, per repo convention (package-local
// Config struct, interface-typed fields, New fills zero values).
type Config struct {
	Session *tui.Session
	// Lister is optional — used only to refresh the pod's live restart
	// count when synthesizing a reconnect boundary entry. A nil Lister
	// falls back to the Restarts count captured when the screen opened.
	Lister   resources.RawLister
	Pod      SelectedPod
	Streamer kube.PodLogStreamer
	// InitialContainer names the container to start streaming (poddetail's
	// 'l' opens on whichever row the CONTAINERS grid had selected) — empty
	// falls back to index 0, unchanged from before this field existed. A
	// name that isn't in Pod.Containers (stale selection, race with a pod
	// update) falls back the same way rather than erroring.
	InitialContainer string
	MaxEntries       int
	TailLines        int64
}

type Model struct {
	width, height int

	session *tui.Session
	lister  resources.RawLister

	pod          SelectedPod
	containerIdx int
	sinceIdx     int

	view          LogViewState
	buffer        LogBuffer
	stream        StreamState
	lastError     string
	permDenied    bool
	waitingReason string // set only while stream == StreamWaitingForContainer
	feedback      string
	streamer      kube.PodLogStreamer
	tailLines     int64

	streamCancel context.CancelFunc
	streamCh     chan tea.Msg
	streamID     int

	filterActive bool
	filterInput  textinput.Model

	rateGen        int
	linesSinceTick int
	lastRate       int

	spinner spinner.Model
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

	containerIdx := 0
	for i, name := range cfg.Pod.Containers {
		if name == cfg.InitialContainer {
			containerIdx = i
			break
		}
	}

	return Model{
		width:        tui.DefaultWidth,
		height:       tui.DefaultHeight,
		session:      cfg.Session,
		lister:       cfg.Lister,
		pod:          cfg.Pod,
		containerIdx: containerIdx,
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
		spinner:   components.NewSpinner(),
	}
}

// FromPod builds podlogs for pod, opened from browse/poddetail — the shape
// every OpenLogsFunc caller in internal/app wires against. initialContainer
// is the container to start streaming (poddetail's CONTAINERS grid
// selection) — empty falls back to index 0.
func FromPod(session *tui.Session, lister resources.RawLister, pod kube.Pod, initialContainer string, streamer kube.PodLogStreamer) Model {
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
		InitialContainer: initialContainer,
		Streamer:         streamer,
	})
}

func (m Model) Init() tea.Cmd { return nil }

// Start begins streaming — called once by the OpenLogsFunc caller
// alongside pushing the screen (mirrors poddetail/nodedetail's Init()
// pattern, kept as an explicit method since browse already calls it that
// way and changing the call site isn't otherwise needed).
func (m *Model) Start() tea.Cmd { return m.beginStream(StreamLoading) }

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
	// Trim before append so a saturated buffer reuses its backing array. The
	// old append-to-nil trim allocated and copied all 5,000 entries for every
	// incoming line after saturation.
	if over := len(b.Entries) - b.MaxEntries + 1; over > 0 {
		copy(b.Entries, b.Entries[over:])
		newLen := len(b.Entries) - over
		clear(b.Entries[newLen:])
		b.Entries = b.Entries[:newLen]
		b.DroppedCount += over
	}
	b.Entries = append(b.Entries, entry)
}

func (m *Model) appendEntry(entry LogEntry) {
	// Once following has enough rows to scroll, advance the bottom offset by
	// only the rows added/evicted here. Re-laying out the full 5,000-entry
	// buffer for every streamed line would make wrapping quadratic at steady
	// state. Before the first scroll point, clampOffsets performs the small
	// full calculation needed to detect when the viewport becomes full.
	droppedRows := 0
	oldViewportHeight := m.entryViewportHeight()
	if over := len(m.buffer.Entries) - m.buffer.MaxEntries + 1; over > 0 {
		for _, dropped := range m.buffer.Entries[:over] {
			if m.entryMatchesFilter(dropped) {
				droppedRows += len(m.visualRows([]LogEntry{dropped}, m.view.Width))
			}
		}
	}
	addedRows := 0
	if m.entryMatchesFilter(entry) {
		addedRows = len(m.visualRows([]LogEntry{entry}, m.view.Width))
	}
	wasScrollableFollow := m.view.AutoScroll && m.view.VerticalOffset > 0

	m.buffer.Append(entry)
	m.linesSinceTick++
	if m.stream != StreamError {
		m.stream = StreamStreaming
	}
	m.feedback = ""
	switch {
	case wasScrollableFollow:
		// A state/strip change or the first persistent "older lines dropped"
		// notice can also change the number of physical rows available.
		viewportDelta := oldViewportHeight - m.entryViewportHeight()
		m.view.VerticalOffset = max(0, m.view.VerticalOffset+addedRows-droppedRows+viewportDelta)
	case m.view.AutoScroll:
		m.clampOffsets()
	case droppedRows > 0:
		// Keep the same logical content anchored when the bounded buffer evicts
		// physical rows above a paused viewport.
		m.view.VerticalOffset = max(0, m.view.VerticalOffset-droppedRows)
	}
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
	out := make([]LogEntry, 0, len(m.buffer.Entries))
	for _, e := range m.buffer.Entries {
		if m.entryMatchesFilter(e) {
			out = append(out, e)
		}
	}
	return out
}

func (m Model) entryMatchesFilter(entry LogEntry) bool {
	query := strings.ToLower(m.filterInput.Value())
	return query == "" || entry.Boundary || strings.Contains(strings.ToLower(entry.Message), query)
}

func (m *Model) clampOffsets() {
	if m.view.VerticalOffset < 0 {
		m.view.VerticalOffset = 0
	}
	maxOff := m.maxVerticalOffset()
	if m.view.AutoScroll {
		m.view.VerticalOffset = maxOff
	} else if m.view.VerticalOffset > maxOff {
		m.view.VerticalOffset = maxOff
	}
	if m.view.HorizontalOffset < 0 || m.view.Wrap {
		m.view.HorizontalOffset = 0
	}
}

func (m Model) maxVerticalOffset() int {
	visible := m.entryViewportHeight()
	entries := m.filteredEntries()
	total := len(m.visualRows(entries, m.view.Width))
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
		if m.filterActive {
			return 1
		}
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
		// permDenied, not a re-classification of lastError: that field is
		// only the error's *text*, and rebuilding an error from it threw away
		// the *apierrors.StatusError, so IsPermissionError could never reach
		// its apierrors.IsForbidden path here — only its substring fallback.
		// update.go classifies the real error the moment it arrives.
		if m.permDenied {
			return tui.TaskStatePermissionDenied
		}
		return tui.TaskStateError
	case StreamLoading, StreamReconnecting, StreamIdle, StreamWaitingForContainer:
		return tui.TaskStateLoading
	default:
		return tui.TaskStateReady
	}
}
