package objectdetail

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/components"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case kube.ResourceChangedMsg:
		if msg.Kind == m.kind && m.lister != nil {
			return m, m.load()
		}
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
		m.actions.SetOffline(m.conn.Offline())
	case loadedMsg:
		return m.applyLoaded(msg)
	case components.SpinnerTickMsg:
		if m.state != tui.TaskStateLoading {
			return m, nil
		}
		m.spinner = m.spinner.Advance()
		return m, components.SpinnerTick()
	case actions.ResultMsg:
		m.actions.HandleResult(msg)
		if msg.Err == nil {
			return m, m.load()
		}
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// applyLoaded is load()'s result handler. When the object has neither
// conditions nor events, it redirects straight to tasks/yamlview instead of
// transitioning to ready (docs/design README.md §14d: "an empty detail
// screen is worse than the manifest") — root's generic task-swap plumbing
// (tui/model.go's sameTask check) makes this read as "↵ opened YAML"
// directly, with no empty-screen flash, since this Update call replaces the
// active task before any render of this frame happens. An events-fetch
// error is a distinct, non-empty state (unknown, not "no events") and never
// triggers the redirect.
func (m *Model) applyLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.state = tui.TaskStateError
		if kube.IsPermissionError(msg.err) {
			m.state = tui.TaskStatePermissionDenied
		}
		m.feedback = msg.err.Error()
		return m, nil
	}
	if !msg.found {
		m.gone = true
		m.state = tui.TaskStateReady
		m.feedback = ""
		return m, nil
	}
	if len(msg.conditions) == 0 && len(msg.events) == 0 && msg.eventsErr == nil && m.openYAML != nil {
		task, cmd := m.openYAML(m.kind, m.namespace, m.name, m.width, m.height)
		if task != nil {
			return task, cmd
		}
	}
	m.obj = msg.obj
	m.row = msg.row
	m.conditions = msg.conditions
	m.eventRows = msg.events
	m.eventsErr = msg.eventsErr
	m.found = true
	m.state = tui.TaskStateReady
	m.feedback = ""
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.actions.Active() {
		return m.updateConfirmKey(msg)
	}
	if m.gone {
		// "object gone ⇒ banner + auto-back after keypress" (mirrors
		// poddetail's own §5a behavior).
		return m, func() tea.Msg { return tui.BackMsg{} }
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "j":
		return m, m.moveSibling(1)
	case "k":
		return m, m.moveSibling(-1)
	case "y":
		if task, cmd, ok := m.openSelectedYAML(); ok {
			return task, cmd
		}
	case "e":
		if task, cmd, ok := m.openSelectedEvents(); ok {
			return task, cmd
		}
	case "ctrl+d":
		return m, m.beginDelete()
	}
	return m, nil
}

// updateConfirmKey/updateModalConfirmKey mirror poddetail's own — TierModal
// (the type-the-name PROD modal) gets its own key handling; TierInline/
// TierNone stay the simple y/n/esc prompt.
func (m *Model) updateConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.actions.Tier() == actions.TierModal {
		return m.updateModalConfirmKey(msg)
	}
	switch msg.String() {
	case "y":
		return m, m.actions.Confirm()
	case "n", "esc":
		m.actions.Cancel()
	}
	return m, nil
}

func (m *Model) updateModalConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.actions.Cancel()
	case "enter":
		return m, m.actions.Confirm()
	case "backspace":
		m.actions.Backspace()
	case "ctrl+k":
		m.actions.Escalate()
	default:
		if msg.Text != "" {
			m.actions.TypeRune(msg.Text)
		}
	}
	return m, nil
}

// isProd mirrors poddetail's own — same source (7a's context palette PROD
// tag, internal/tui/context.go).
func (m Model) isProd() bool {
	if m.session == nil {
		return false
	}
	return m.session.Config.IsProd(m.session.Location.Context)
}

// moveSibling shifts to the next/prev object in browse's ordered list
// without leaving detail — mirrors poddetail's own moveSibling, generalized
// to any kind.
func (m *Model) moveSibling(delta int) tea.Cmd {
	if len(m.siblings) == 0 {
		return nil
	}
	next := m.siblingIndex + delta
	if next < 0 || next >= len(m.siblings) {
		return nil
	}
	m.siblingIndex = next
	m.name = m.siblings[next]
	m.gone = false
	m.found = false
	m.obj = nil
	m.row = resources.Row{}
	m.conditions = nil
	m.eventRows = nil
	m.eventsErr = nil
	m.state = tui.TaskStateLoading
	m.feedback = "Loading " + m.name + "..."
	if m.lister == nil {
		m.state = tui.TaskStateError
		m.feedback = "no cluster connection"
		return nil
	}
	return tea.Batch(m.load(), components.SpinnerTick())
}

// openSelectedYAML pushes 8a for the loaded object.
func (m Model) openSelectedYAML() (tea.Model, tea.Cmd, bool) {
	if m.openYAML == nil || !m.found {
		return nil, nil, false
	}
	task, cmd := m.openYAML(m.kind, m.namespace, m.name, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedEvents pushes 9b object-scoped for the loaded object.
func (m Model) openSelectedEvents() (tea.Model, tea.Cmd, bool) {
	if m.openEvents == nil || !m.found {
		return nil, nil, false
	}
	task, cmd := m.openEvents(m.kind, m.namespace, m.name, m.width, m.height)
	return task, cmd, task != nil
}

// beginDelete confirms deleting the object — inline y/N in non-prod
// contexts, the full type-the-name modal in PROD (verbs.TierFor) — normal
// tier resolution, no special-casing (only the CustomResourceDefinition
// list's own delete forces modal unconditionally, in browse/delete.go).
// Owner rides along for the modal's "will be recreated" line when the
// object has an ownerReference.
func (m *Model) beginDelete() tea.Cmd {
	if !m.found {
		return nil
	}
	return m.actions.Begin(verbs.TierFor(verbs.Delete, m.isProd()), tui.TaskAction{
		ID:    "delete-" + string(m.kind) + "-" + m.namespace + "/" + m.name,
		Label: "Delete " + singularDisplay(m.desc.Display) + " " + m.name + "?",
		Owner: firstOwnerRef(m.obj),
		Scope: tui.TaskScope{
			ResourceKind: string(m.kind),
			ResourceName: m.name,
			Namespace:    m.namespace,
			Verb:         "delete",
			IsMutating:   true,
		},
	})
}

// singularDisplay strips a trailing "s" off a Descriptor.Display plural
// (e.g. "certificates" -> "certificate") for the delete confirm's label —
// good enough for the regular CRD plurals kute never guesses columns for
// either; an irregular plural just reads slightly off, not wrong.
func singularDisplay(plural string) string {
	return strings.TrimSuffix(plural, "s")
}

// firstOwnerRef renders obj's first ownerReference as "Kind/Name" for the
// delete modal's "will be recreated" line — "" when there is none, same as
// poddetail's Owner field for a pod with no controller.
func firstOwnerRef(obj *unstructured.Unstructured) string {
	if obj == nil {
		return ""
	}
	refs := obj.GetOwnerReferences()
	if len(refs) == 0 {
		return ""
	}
	return refs[0].Kind + "/" + refs[0].Name
}
