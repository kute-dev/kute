package whocan

import (
	"context"

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
		if isRBACKind(msg.Kind) && m.rbac != nil {
			return m, m.load()
		}
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
	case tui.SwitchNamespaceMsg:
		if msg.Namespace != m.namespace {
			m.namespace = msg.Namespace
			return m, m.reload()
		}
	case tui.SetWhoCanVerbMsg:
		if msg.Verb != m.verb {
			m.verb = msg.Verb
			return m, m.reload()
		}
	case tui.SetWhoCanResourceMsg:
		if msg.Resource != m.resource {
			m.resource = msg.Resource
			return m, m.reload()
		}
	case loadedMsg:
		return m.applyLoaded(msg)
	case spinner.TickMsg:
		if m.state != tui.TaskStateLoading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// isRBACKind reports whether kind is one of the four RBAC kinds whose
// change should trigger a re-resolve — every other kind change is
// irrelevant to who-can's own binding graph.
func isRBACKind(kind kube.ResourceKind) bool {
	switch kind {
	case kube.KindRole, kube.KindRoleBinding, kube.KindClusterRole, kube.KindClusterRoleBinding:
		return true
	default:
		return false
	}
}

// reload puts the model back into the loading state and re-issues load() —
// called whenever the query's verb/resource/namespace slot changes.
func (m *Model) reload() tea.Cmd {
	m.reloadEpoch++
	m.state = tui.TaskStateLoading
	m.feedback = "Resolving who can " + m.verb + " " + m.resource + "..."
	m.rows = nil
	m.selected = 0
	return tea.Batch(m.load(), m.spinner.Tick)
}

func (m Model) load() tea.Cmd {
	epoch := m.reloadEpoch
	rbac := m.rbac
	query := kube.WhoCanQuery{Verb: m.verb, Resource: m.resource, Namespace: m.namespace}
	return func() tea.Msg {
		result, err := rbac.WhoCan(context.Background(), query)
		return loadedMsg{epoch: epoch, result: result, err: err}
	}
}

func (m *Model) applyLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	if msg.epoch != m.reloadEpoch {
		return m, nil
	}
	if msg.err != nil {
		m.state = tui.TaskStateError
		if kube.IsPermissionError(msg.err) {
			m.state = tui.TaskStatePermissionDenied
		}
		m.feedback = msg.err.Error()
		return m, nil
	}
	m.result = msg.result
	m.rows = buildRows(msg.result)
	m.state = tui.TaskStateReady
	if len(m.rows) == 0 {
		m.state = tui.TaskStateEmpty
	}
	m.feedback = ""
	if m.selected >= len(m.rows) {
		m.selected = max(len(m.rows)-1, 0)
	}
	return m, nil
}

// buildRows promotes the current user's own verdict to a pinned first row
// (docs/design README.md §22a). The pinned row always names CurrentUser
// itself (not whichever subject actually matched) since the grant is very
// often a Group the user belongs to rather than a "User" subject bearing
// their own name (e.g. a client cert's Organization — "system:masters" —
// rather than its CommonName), so kube.ResolveWhoCan's own
// CurrentUserVia/CurrentUserClusterScope (which already account for both
// shapes) are the source of truth here, not a re-derived Subjects scan.
// Only an exact "User" kind match is dropped from the plain list below it
// (the same person, never shown twice) — a Group row that happens to cover
// the current user stays listed as-is, since it also covers every other
// member of that group.
func buildRows(result kube.WhoCanResult) []whoCanRow {
	rows := make([]whoCanRow, 0, len(result.Subjects)+1)
	if result.CurrentUser != "" {
		rows = append(rows, whoCanRow{
			pinned:  true,
			granted: result.CurrentUserGranted,
			subject: kube.WhoCanSubject{
				Name:         result.CurrentUser,
				Kind:         "User",
				Via:          result.CurrentUserVia,
				ClusterScope: result.CurrentUserClusterScope,
			},
		})
	}
	for _, s := range result.Subjects {
		if result.CurrentUser != "" && s.Kind == "User" && s.Name == result.CurrentUser {
			continue
		}
		rows = append(rows, whoCanRow{subject: s})
	}
	return rows
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		if task, cmd, ok := m.openSelectedBinding(); ok {
			return task, cmd
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

// openSelectedBinding is 22a's "↵ opens the binding's YAML" — pushes 8a for
// the resolved subject row's backing RoleBinding/ClusterRoleBinding. A
// no-op for the pinned miss row (BindingName is empty: there's no real
// binding to open when the current user has no matching grant at all).
func (m Model) openSelectedBinding() (tea.Model, tea.Cmd, bool) {
	row, ok := m.selectedRow()
	if !ok || m.openYAML == nil || row.subject.BindingName == "" {
		return nil, nil, false
	}
	task, cmd := m.openYAML(row.subject.BindingKind, row.subject.BindingNamespace, row.subject.BindingName, m.width, m.height)
	return task, cmd, task != nil
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
