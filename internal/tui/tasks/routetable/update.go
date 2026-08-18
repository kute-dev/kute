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
	case kube.ResourceChangedMsg:
		// This screen resolves every row's backend live, through the
		// Service and Pod caches (and Secrets, for a TLS listener's cert
		// expiry). It previously loaded once on Init and never again, so a
		// backend that changed — or a cache that only finished filling
		// after the screen opened — left BACKENDS wrong until the user
		// backed out and came in again.
		if m.reloadsOn(msg.Kind) && m.lister != nil {
			return m, m.load()
		}
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
		m.now = time.Now()
	case tui.CacheSyncRetryMsg:
		if msg.Gen == m.syncRetryGen && m.lister != nil {
			return m, m.load()
		}
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
		// findUnstructured's own "<kind> <name> not found" (load.go) is a
		// plain fmt.Errorf, indistinguishable on its face from the object's
		// own cache having not filled yet or come back Forbidden — both of
		// which read exactly like "not found" to a ListRaw scan for one
		// name. Ask the cache directly before believing the string: a
		// genuine 404 (the object really was deleted) still ends up at the
		// same TaskStateError below once both checks come back clean.
		if !tui.KindsSynced(m.lister, m.namespace, m.kind) {
			m.syncRetryGen++
			return m, tui.ScheduleCacheSyncRetry(m.syncRetryGen)
		}
		if kerr := tui.KindsError(m.lister, m.namespace, m.kind); kerr != nil {
			if kube.IsPermissionError(kerr) {
				m.state = tui.TaskStatePermissionDenied
			} else {
				m.state = tui.TaskStateError
			}
			m.feedback = kerr.Error()
			return m, nil
		}
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
		// This screen resolves every row through the pushed-from object's
		// own cache (m.kind/m.namespace) plus the Service/Pod caches every
		// flavor's backend resolution reads (reloadsOn's own kind set,
		// minus Secret/Gateway — TLS facts and the parent-Gateway strip
		// already degrade gracefully and must not hard-gate the table).
		// Without this, a cache that's merely still filling — or Forbidden
		// — rendered as "no routes" instead of loading/permission-denied.
		if !tui.KindsSynced(m.lister, m.namespace, m.kind, kube.KindService, kube.KindPod) {
			m.syncRetryGen++
			return m, tui.ScheduleCacheSyncRetry(m.syncRetryGen)
		}
		if err := tui.KindsError(m.lister, m.namespace, m.kind, kube.KindService, kube.KindPod); err != nil {
			if kube.IsPermissionError(err) {
				m.state = tui.TaskStatePermissionDenied
			} else {
				m.state = tui.TaskStateError
			}
			m.feedback = err.Error()
			return m, nil
		}
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
// tui.GotoResource/GotoKind, the same navigation poddetail/events/the root
// shell's palette Enter all use.
func (m Model) openSelectedEnter() (tea.Cmd, bool) {
	if m.flavor == flavorGateway {
		filter, ok := m.selectedListenerRouteFilter()
		if !ok {
			return nil, false
		}
		return tui.GotoKind(m.session, kube.KindHTTPRoute, filter), true
	}
	row, ok := m.selectedRouteRow()
	if !ok || row.backendName == "" {
		return nil, false
	}
	return tui.GotoResource(m.session, kube.KindService, row.backendNS, row.backendName), true
}

// openParentGateway resolves 'p' on an HTTPRoute (§23b: "p opens the
// Gateway") — a no-op on any other flavor or before status.parents has
// resolved a parent.
func (m Model) openParentGateway() (tea.Cmd, bool) {
	if m.flavor != flavorRoute || m.parentGatewayName == "" {
		return nil, false
	}
	return tui.GotoResource(m.session, kube.KindGateway, m.parentGatewayNS, m.parentGatewayName), true
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
	return tui.GotoResource(m.session, kube.KindSecret, m.namespace, name), true
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

// reloadsOn reports whether a change to kind affects what this table shows:
// the routing object itself, or one of the caches its backend and cert
// columns are resolved through.
func (m Model) reloadsOn(kind kube.ResourceKind) bool {
	switch kind {
	case m.kind, kube.KindService, kube.KindPod, kube.KindSecret:
		return true
	case kube.KindGateway:
		// A route's parent listener supplies its hostname and TLS detail.
		return m.kind != kube.KindGateway
	}
	return false
}
