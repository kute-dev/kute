package events

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
		if msg.Kind == kube.KindEvent && m.events != nil {
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

func (m *Model) applyLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.state = tui.TaskStateError
		if kube.IsPermissionError(msg.err) {
			m.state = tui.TaskStatePermissionDenied
		}
		m.feedback = msg.err.Error()
		return m, nil
	}
	m.groups = msg.groups
	m.failing = msg.failing
	m.fetchedAt = time.Now()
	m.recomputeVisible()
	m.state = tui.TaskStateReady
	if len(m.groups) == 0 {
		m.state = tui.TaskStateEmpty
	}
	m.feedback = ""
	return m, nil
}

// recomputeVisible rebuilds m.rows from m.groups: window + filter-query
// applied first, then partitioned warnings-first with normal groups folded
// into one summary row unless normalExpanded (docs/design README.md §9b).
// Both Update's keyboard handlers (window/fold/filter toggles) and Render's
// summary-strip counts read m.rows, so there's exactly one place events are
// windowed/filtered/folded — never two paths that could disagree.
func (m *Model) recomputeVisible() {
	cutoff := time.Time{}
	if m.window > 0 {
		cutoff = m.fetchedAt.Add(-m.window)
	}

	var warnings, normal []kube.EventGroup
	baseline, matched := 0, 0
	for _, g := range m.groups {
		if !cutoff.IsZero() && g.LastSeen.Before(cutoff) {
			continue
		}
		if g.Type != "Warning" && m.warningsOnly {
			continue
		}
		baseline++
		if m.filterQuery != "" && !matchesQuery(g, m.filterQuery) {
			continue
		}
		matched++
		if g.Type == "Warning" {
			warnings = append(warnings, g)
		} else {
			normal = append(normal, g)
		}
	}
	m.filterBaselineGroups, m.filterMatchedGroups = baseline, matched

	rows := make([]displayRow, 0, len(warnings)+1)
	for _, g := range warnings {
		rows = append(rows, displayRow{kind: rowGroup, group: g})
	}
	if len(normal) > 0 {
		if m.normalExpanded {
			for _, g := range normal {
				rows = append(rows, displayRow{kind: rowGroup, group: g})
			}
		} else {
			rows = append(rows, displayRow{kind: rowFolded, folded: normal})
		}
	}
	m.rows = rows
	if m.selected >= len(m.rows) {
		m.selected = max(len(m.rows)-1, 0)
	}
}

func matchesQuery(g kube.EventGroup, query string) bool {
	q := strings.ToLower(query)
	return strings.Contains(strings.ToLower(g.Reason), q) ||
		strings.Contains(strings.ToLower(g.Object), q) ||
		strings.Contains(strings.ToLower(g.Message), q)
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.filterActive {
		return m.updateFilterKey(msg)
	}
	switch msg.String() {
	case "ctrl+q", "ctrl+c":
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
	case "tab":
		m.normalExpanded = !m.normalExpanded
		m.recomputeVisible()
	case "w":
		m.warningsOnly = !m.warningsOnly
		m.recomputeVisible()
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
	// alt+j/alt+k are safe alongside plain j/k typing into the query — an
	// alt-modified key never carries Text (charm.land/bubbletea/v2's
	// Key.Text doc), so it can't reach the default typing branch below.
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
	for i, w := range eventWindows {
		if w == m.window {
			idx = i
			break
		}
	}
	m.window = eventWindows[(idx+1)%len(eventWindows)]
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
// lets every keystroke reach events' own key handling (mirrors
// browse.CapturingInput).
func (m Model) CapturingInput() bool {
	return m.filterActive
}
