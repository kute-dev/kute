package routetable

import (
	"context"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
		m.now = time.Now()
	case loadedMsg:
		return m.applyLoaded(msg)
	case spinner.TickMsg:
		if m.state != tui.TaskStateLoading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.now = time.Now()
		return m, cmd
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
	m.flavor = msg.flavor
	m.ingressClass = msg.ingressClass
	m.ingressHostCount = msg.ingressHostCount
	m.tlsFacts = msg.tlsFacts
	m.rows = msg.rows
	m.parentText = msg.parentText
	m.parentAttached = msg.parentAttached
	m.parentGatewayNS = msg.parentGatewayNS
	m.parentGatewayName = msg.parentGatewayName
	m.parentListenerText = msg.parentListenerText
	m.routeHostText = msg.routeHostText
	m.routeRuleCount = msg.routeRuleCount
	m.gatewayClass = msg.gatewayClass
	m.listeners = msg.listeners

	if m.rowCount() == 0 {
		m.state = tui.TaskStateEmpty
	} else {
		m.state = tui.TaskStateReady
	}
	m.feedback = ""
	m.selected = clamp(m.selected, 0, m.rowCount()-1)
	m.clampOffset()
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+q", "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "tab":
		if m.flavor == flavorIngress && len(m.tlsFacts) > 0 {
			m.tlsFocused = !m.tlsFocused
			m.tlsSelected = clamp(m.tlsSelected, 0, len(m.tlsFacts)-1)
		}
	case "up", "k":
		if m.tlsFocused {
			m.tlsSelected = clamp(m.tlsSelected-1, 0, len(m.tlsFacts)-1)
			return m, nil
		}
		m.moveSelection(-1)
	case "down", "j":
		if m.tlsFocused {
			m.tlsSelected = clamp(m.tlsSelected+1, 0, len(m.tlsFacts)-1)
			return m, nil
		}
		m.moveSelection(1)
	case "enter":
		if m.tlsFocused {
			if cmd, ok := m.openSelectedTLSSecret(); ok {
				return m, cmd
			}
			return m, nil
		}
		if cmd, ok := m.openSelectedEnter(); ok {
			return m, cmd
		}
	case "p":
		if cmd, ok := m.openParentGateway(); ok {
			return m, cmd
		}
	case "y":
		if cmd, ok := m.copySelectedURL(); ok {
			return m, cmd
		}
	case "Y":
		if cmd, ok := m.copyYAML(); ok {
			return m, cmd
		}
	case "e":
		if task, cmd, ok := m.openObjectEvents(); ok {
			return task, cmd
		}
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	n := m.rowCount()
	if n == 0 {
		m.selected, m.offset = 0, 0
		return
	}
	m.selected = clamp(m.selected+delta, 0, n-1)
	m.clampOffset()
}

// clampOffset keeps the selected row within the table's rendered viewport —
// mirrors nodedetail's own clampOffset/tableDataRows pattern.
func (m *Model) clampOffset() {
	rows := m.tableDataRows()
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+rows {
		m.offset = m.selected - rows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
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

// selectedListenerRouteFilter computes the destination HTTPRoute list's
// filter query for the currently selected Gateway listener (§23b: "↵ on a
// listener filters to attached routes"): the listener's own hostname when it
// has one, or this Gateway's own ATTACHED-cell text ("gw/<name>") for a
// wildcard listener, which can't be told apart from another one on the same
// Gateway by hostname alone. ok is false with no selected listener.
func (m Model) selectedListenerRouteFilter() (string, bool) {
	listener, ok := m.selectedListener()
	if !ok {
		return "", false
	}
	if listener.hostname != "" {
		return listener.hostname, true
	}
	return "gw/" + m.name, true
}

// openSelectedEnter resolves '↵': for the Ingress/HTTPRoute flavors, jump to
// the selected row's backend Service (docs/design README.md §23a/§23b); for
// Gateway, jump to the HTTPRoute list pre-filtered to the selected listener's
// hostname (§23b: "↵ on a listener filters to attached routes") — or, for a
// wildcard listener with no hostname of its own, to this Gateway's own
// attached routes (its ATTACHED cell text, "gw/<name>") rather than every
// HTTPRoute in the namespace, since a wildcard listener can't be told apart
// from another one on the same Gateway by hostname alone. Both go through
// the same BackMsg+GotoResourceMsg/GotoKindMsg sequence poddetail/events
// already use to leave the current screen and ask whatever pushed it to
// jump.
func (m Model) openSelectedEnter() (tea.Cmd, bool) {
	if m.flavor == flavorGateway {
		filter, ok := m.selectedListenerRouteFilter()
		if !ok {
			return nil, false
		}
		return tea.Sequence(
			func() tea.Msg { return tui.BackMsg{} },
			func() tea.Msg { return tui.GotoKindMsg{Kind: kube.KindHTTPRoute, Filter: filter} },
		), true
	}
	row, ok := m.selectedRouteRow()
	if !ok || row.backendName == "" {
		return nil, false
	}
	ns, name := row.backendNS, row.backendName
	return tea.Sequence(
		func() tea.Msg { return tui.BackMsg{} },
		func() tea.Msg { return tui.GotoResourceMsg{Kind: kube.KindService, Namespace: ns, Name: name} },
	), true
}

// openParentGateway resolves 'p' on an HTTPRoute (§23b: "p opens the
// Gateway") — a no-op on any other flavor or before status.parents has
// resolved a parent.
func (m Model) openParentGateway() (tea.Cmd, bool) {
	if m.flavor != flavorRoute || m.parentGatewayName == "" {
		return nil, false
	}
	ns, name := m.parentGatewayNS, m.parentGatewayName
	return tea.Sequence(
		func() tea.Msg { return tui.BackMsg{} },
		func() tea.Msg { return tui.GotoResourceMsg{Kind: kube.KindGateway, Namespace: ns, Name: name} },
	), true
}

// openSelectedTLSSecret resolves '↵' on a focused TLS strip fact (§23a: "a
// strip above the keybar names each secret — ↵ there jumps to it") — the
// referenced Secret is in the same namespace as the viewed Ingress.
func (m Model) openSelectedTLSSecret() (tea.Cmd, bool) {
	if m.flavor != flavorIngress || m.tlsSelected < 0 || m.tlsSelected >= len(m.tlsFacts) {
		return nil, false
	}
	name := m.tlsFacts[m.tlsSelected].secretName
	if name == "" {
		return nil, false
	}
	ns := m.namespace
	return tea.Sequence(
		func() tea.Msg { return tui.BackMsg{} },
		func() tea.Msg { return tui.GotoResourceMsg{Kind: kube.KindSecret, Namespace: ns, Name: name} },
	), true
}

// copySelectedURL resolves 'y' on an Ingress row (§23a: "y copies the full
// URL") — a no-op on any other flavor, or a row with no resolved URL.
func (m Model) copySelectedURL() (tea.Cmd, bool) {
	if m.flavor != flavorIngress {
		return nil, false
	}
	row, ok := m.selectedRouteRow()
	if !ok || row.url == "" {
		return nil, false
	}
	return tea.SetClipboard(row.url), true
}

// copyYAML resolves 'Y' (§23a/§23b: "Y copies the full yaml") — fetches the
// viewed object's own YAML and puts it straight on the clipboard, rather than
// pushing 8a (the same screen-local 'y'-reuse precedent as CopyRouteURL).
func (m Model) copyYAML() (tea.Cmd, bool) {
	if m.yaml == nil {
		return nil, false
	}
	kind, ns, name, reader, timeout := m.kind, m.namespace, m.name, m.yaml, m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		text, _, err := reader.GetYAML(ctx, kind, ns, name)
		if err != nil {
			return nil
		}
		return tea.SetClipboard(text)()
	}, true
}

// openObjectEvents resolves 'e' (§23a/§23b keybar: "events") — pushes 9b
// object-scoped for the Ingress/HTTPRoute/Gateway this screen is viewing.
func (m Model) openObjectEvents() (tea.Model, tea.Cmd, bool) {
	if m.openEvents == nil {
		return nil, nil, false
	}
	task, cmd := m.openEvents(m.kind, m.namespace, m.name, m.width, m.height)
	return task, cmd, true
}
