package timeline

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case kube.ResourceChangedMsg:
		if isTimelineSource(msg.Kind) && m.events != nil {
			return m, m.load()
		}
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
	case loadedMsg:
		return m.applyLoaded(msg)
	case components.SpinnerTickMsg:
		if m.state != tui.TaskStateLoading {
			return m, nil
		}
		m.spinner = m.spinner.Advance()
		return m, components.SpinnerTick()
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// isTimelineSource reports whether a watch event could change the merged
// feed — Events directly, Pods (restarts) and ReplicaSets (rollout
// revisions) indirectly.
func isTimelineSource(kind kube.ResourceKind) bool {
	return kind == kube.KindEvent || kind == kube.KindPod || kind == kube.KindReplicaSet
}

func (m *Model) applyLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.state = tui.TaskStateError
		if kube.IsPermissionError(msg.err) {
			m.state = tui.TaskStatePermissionDenied
		}
		m.feedback = msg.err.Error()
		return m, nil
	}
	m.entries = msg.entries
	m.rail = msg.rail
	m.railDeployment = msg.railDeployment
	m.fetchedAt = time.Now()
	m.recomputeVisible()
	m.state = tui.TaskStateReady
	if len(m.rows) == 0 && len(m.rail) == 0 {
		// A 16b revision rail is still worth showing even when nothing
		// happened in the feed's own window — it's not "empty" until there's
		// truly nothing on screen.
		m.state = tui.TaskStateEmpty
	}
	m.feedback = ""
	return m, nil
}

// recomputeVisible rebuilds m.rows from m.entries: window + filter-query
// applied — the one place the feed is windowed/filtered, so the summary
// strip's counts (view.go) and Body's row walk can never disagree about
// what's currently shown (mirrors tasks/events' own recomputeVisible).
func (m *Model) recomputeVisible() {
	cutoff := time.Time{}
	if m.window > 0 {
		cutoff = m.fetchedAt.Add(-m.window)
	}

	rows := make([]kube.TimelineEntry, 0, len(m.entries))
	for _, e := range m.entries {
		if !cutoff.IsZero() && e.Time.Before(cutoff) {
			continue
		}
		if m.filterQuery != "" && !matchesQuery(e, m.filterQuery) {
			continue
		}
		rows = append(rows, e)
	}
	m.rows = rows
	if m.selected >= len(m.rows) {
		m.selected = max(len(m.rows)-1, 0)
	}
}

func matchesQuery(e kube.TimelineEntry, query string) bool {
	q := strings.ToLower(query)
	return strings.Contains(strings.ToLower(e.Reason), q) ||
		strings.Contains(strings.ToLower(e.Object), q) ||
		strings.Contains(strings.ToLower(e.Message), q)
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.filterActive {
		return m.updateFilterKey(msg)
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "enter":
		if cmd, ok := m.openSelectedObject(); ok {
			return m, cmd
		}
	case "t":
		m.cycleWindow()
	case "/":
		if m.state == tui.TaskStateReady || m.state == tui.TaskStateEmpty {
			m.filterActive = true
		}
	}
	return m, nil
}

func (m *Model) updateFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterActive = false
		m.filterQuery = ""
		m.recomputeVisible()
	case "backspace":
		if len(m.filterQuery) > 0 {
			m.filterQuery = m.filterQuery[:len(m.filterQuery)-1]
			m.recomputeVisible()
		}
	// alt+j/alt+k are safe alongside plain j/k typing into the query, same
	// reasoning as tasks/events' own filter handler.
	case "up", "alt+k":
		m.moveSelection(-1)
	case "down", "alt+j":
		m.moveSelection(1)
	default:
		if msg.Text != "" {
			m.filterQuery += msg.Text
			m.recomputeVisible()
		}
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	if len(m.rows) == 0 {
		m.selected = 0
		return
	}
	m.selected = clamp(m.selected+delta, 0, len(m.rows)-1)
}

func (m *Model) cycleWindow() {
	idx := 0
	for i, w := range windowSteps {
		if w == m.window {
			idx = i
			break
		}
	}
	m.window = windowSteps[(idx+1)%len(windowSteps)]
	m.recomputeVisible()
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// CapturingInput reports whether the filter box is open, so the root shell
// lets every keystroke reach timeline's own key handling (mirrors
// browse.CapturingInput).
func (m Model) CapturingInput() bool {
	return m.filterActive
}
