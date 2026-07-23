package configmapdata

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
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
		// A consumer being added/removed elsewhere (or the ConfigMap itself
		// changing out from under us) should refresh both the grid and the
		// consumer strip.
		switch msg.Kind {
		case kube.KindConfigMap, kube.KindDeployment, kube.KindStatefulSet, kube.KindDaemonSet:
			m.reloadEpoch++
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
		return m, m.handleResult(msg)
	case tea.PasteMsg:
		if msg.Content == "" {
			return m, nil
		}
		switch {
		case m.adding != nil:
			a := m.adding
			if a.onValue {
				insertInto(&a.value, &a.valueCursor, msg.Content)
			} else {
				insertInto(&a.key, &a.keyCursor, msg.Content)
			}
		case m.editing != nil:
			insertInto(&m.editing.value, &m.editing.valueCursor, msg.Content)
		case m.multiline != nil:
			insertMultiline(m.multiline, msg.Content)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
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
	if !msg.found {
		m.state = tui.TaskStateError
		m.feedback = fmt.Sprintf("configmap %s/%s not found", m.namespace, m.name)
		return m, nil
	}
	m.keys = msg.keys
	m.consumers = msg.consumers
	m.state = tui.TaskStateReady
	m.feedback = ""
	switch {
	case m.focusKey != "":
		if idx, ok := indexOfConfigMapKey(m.keys, m.focusKey); ok {
			m.selected = idx
		} else {
			m.selected = clamp(m.selected, 0, max(len(m.keys)-1, 0))
		}
		m.focusKey = ""
	case m.selected >= len(m.keys):
		m.selected = max(len(m.keys)-1, 0)
	}
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.actions.Active() {
		return m.updateConfirmKey(msg)
	}
	if m.adding != nil {
		return m.updateAddKey(msg)
	}
	if m.editing != nil {
		return m.updateEditKey(msg)
	}
	if m.multiline != nil {
		return m.updateMultilineKey(msg)
	}
	if msg.String() != "esc" {
		// A leftover "updated KEY"/error line from the last commit only
		// answers "what just happened" — stale the moment the user does
		// anything else, same rule secretdata's own updateKey uses.
		m.message, m.lastError = "", ""
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
	case "a", "insert":
		if m.mutator != nil && m.state == tui.TaskStateReady {
			m.adding = &addKeyState{}
		}
	case "enter":
		if m.mutator != nil && m.state == tui.TaskStateReady {
			if row, ok := m.selectedKeyRow(); ok {
				m.beginEditRow(row)
			}
		}
	case "e":
		// 'e' explicitly opens the buffer editor — only meaningful (and only
		// offered in the keybar) on a multi-line row; on a single-line row
		// it's already reachable via '↵'.
		if m.mutator != nil && m.state == tui.TaskStateReady {
			if row, ok := m.selectedKeyRow(); ok && row.multiline() {
				m.multiline = newMultilineEditState(row.key, row.value)
			}
		}
	case "ctrl+d":
		if m.mutator != nil && m.state == tui.TaskStateReady {
			if row, ok := m.selectedKeyRow(); ok {
				return m, m.beginRemove(row)
			}
		}
	}
	return m, nil
}

// beginEditRow opens the right editor for row's shape: the single-line
// in-place edit for a plain value, or the multi-line buffer editor
// (docs/design README.md §27a: "Multi-line keys ... e opens the buffer
// editor") when the value contains a newline — '↵' redirects there too
// rather than leaving multi-line rows with a dead Enter key.
func (m *Model) beginEditRow(row configMapKeyRow) {
	if row.multiline() {
		m.multiline = newMultilineEditState(row.key, row.value)
		return
	}
	m.editing = &editKeyState{key: row.key, original: row.value, value: row.value, valueCursor: len([]rune(row.value))}
}

func (m *Model) updateConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		return m, m.actions.Confirm()
	case "n", "esc":
		m.actions.Cancel()
	}
	return m, nil
}

// updateAddKey routes keys while 'a'/insert's line-insert add row is
// showing — every printable character inserts literally into whichever
// buffer has focus (tab/shift+tab switches). ctrl+r commits and restarts
// every consumer, the same alternate depth an edit's ctrl+r gives.
func (m *Model) updateAddKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	a := m.adding
	switch msg.String() {
	case "esc":
		m.adding = nil
	case "tab":
		a.onValue = true
	case "shift+tab":
		a.onValue = false
	case "left":
		if a.onValue {
			a.valueCursor = max(a.valueCursor-1, 0)
		} else {
			a.keyCursor = max(a.keyCursor-1, 0)
		}
	case "right":
		if a.onValue {
			a.valueCursor = min(a.valueCursor+1, len([]rune(a.value)))
		} else {
			a.keyCursor = min(a.keyCursor+1, len([]rune(a.key)))
		}
	case "backspace":
		if a.onValue {
			deleteBefore(&a.value, &a.valueCursor)
		} else {
			deleteBefore(&a.key, &a.keyCursor)
		}
	case "enter":
		return m, m.commitAdd(false)
	case "ctrl+r":
		return m, m.commitAdd(true)
	default:
		if msg.Text != "" {
			if a.onValue {
				insertInto(&a.value, &a.valueCursor, msg.Text)
			} else {
				insertInto(&a.key, &a.keyCursor, msg.Text)
			}
		}
	}
	return m, nil
}

// updateEditKey routes keys while '↵'s single-line in-place edit is showing
// — the key itself isn't editable here, only its value. esc reverts to the
// original value without applying anything. ctrl+r commits and restarts
// every consumer (docs/design README.md §27a: "ctrl-r chains the apply with
// kubectl rollout restart for every consuming workload").
func (m *Model) updateEditKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	e := m.editing
	switch msg.String() {
	case "esc":
		m.editing = nil
	case "left":
		e.valueCursor = max(e.valueCursor-1, 0)
	case "right":
		e.valueCursor = min(e.valueCursor+1, len([]rune(e.value)))
	case "backspace":
		deleteBefore(&e.value, &e.valueCursor)
	case "enter":
		return m, m.commitEdit(false)
	case "ctrl+r":
		return m, m.commitEdit(true)
	default:
		if msg.Text != "" {
			insertInto(&e.value, &e.valueCursor, msg.Text)
		}
	}
	return m, nil
}

// updateMultilineKey routes keys while the buffer editor (multiline) is
// showing — the "simpler solution" this package substitutes for 17a's own
// shared buffer editor. Arrow keys move the cursor across lines/columns,
// enter inserts a newline (this screen's own commit key is ctrl+o/ctrl+r,
// not enter, since enter has to stay available for the buffer's own
// content — ctrl+o rather than the more conventional-looking ctrl+s since
// ctrl+s is the terminal's own XOFF flow-control key in some environments),
// backspace at column 0 joins with the previous line.
func (m *Model) updateMultilineKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	e := m.multiline
	switch msg.String() {
	case "esc":
		m.multiline = nil
	case "up":
		if e.row > 0 {
			e.row--
			e.col = min(e.col, len([]rune(e.lines[e.row])))
		}
	case "down":
		if e.row < len(e.lines)-1 {
			e.row++
			e.col = min(e.col, len([]rune(e.lines[e.row])))
		}
	case "left":
		switch {
		case e.col > 0:
			e.col--
		case e.row > 0:
			e.row--
			e.col = len([]rune(e.lines[e.row]))
		}
	case "right":
		switch {
		case e.col < len([]rune(e.lines[e.row])):
			e.col++
		case e.row < len(e.lines)-1:
			e.row++
			e.col = 0
		}
	case "enter":
		line := []rune(e.lines[e.row])
		before, after := string(line[:e.col]), string(line[e.col:])
		e.lines[e.row] = before
		tail := append([]string{after}, e.lines[e.row+1:]...)
		e.lines = append(e.lines[:e.row+1], tail...)
		e.row++
		e.col = 0
	case "backspace":
		switch {
		case e.col > 0:
			deleteBefore(&e.lines[e.row], &e.col)
		case e.row > 0:
			prevLen := len([]rune(e.lines[e.row-1]))
			e.lines[e.row-1] += e.lines[e.row]
			e.lines = append(e.lines[:e.row], e.lines[e.row+1:]...)
			e.row--
			e.col = prevLen
		}
	case "ctrl+o":
		return m, m.commitMultiline(false)
	case "ctrl+r":
		return m, m.commitMultiline(true)
	default:
		if msg.Text != "" {
			insertInto(&e.lines[e.row], &e.col, msg.Text)
		}
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	if len(m.keys) == 0 {
		m.selected = 0
		return
	}
	m.selected = clamp(m.selected+delta, 0, len(m.keys)-1)
}

// beginCommit runs the shared "figure out the tier, hand it to
// actions.Controller.Begin" plumbing for an add/edit — restart carries
// through to Scope.ConfigMapRestartConsumers/ConfigMapConsumers so the
// controller's "configmap-data" case (internal/tui/actions/controller.go)
// chains a RolloutRestart per consumer after a successful patch.
func (m *Model) beginCommit(key, value string, isEdit bool, original string, restart bool) tea.Cmd {
	m.pendingCommit = &configMapPendingCommit{
		key: key, value: value, isEdit: isEdit, original: original,
		restartConsumers: restart, consumers: m.consumers,
	}
	m.message, m.lastError = "", ""
	tier := verbs.TierForConfigMapData(m.isProd())
	label := fmt.Sprintf("Add key %s to %s?", key, m.name)
	id := "add-configmap-key-" + m.namespace + "/" + m.name + "/" + key
	if isEdit {
		label = fmt.Sprintf("Update key %s on %s?", key, m.name)
		id = "edit-configmap-key-" + m.namespace + "/" + m.name + "/" + key
	}
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    id,
		Label: label,
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindConfigMap), ResourceName: m.name, Namespace: m.namespace,
			Verb: "configmap-data", IsMutating: true,
			ConfigMapKey: key, ConfigMapValue: value,
			ConfigMapRestartConsumers: restart, ConfigMapConsumers: consumerRefs(m.consumers),
		},
	})
}

// commitAdd executes the add row's key=value — a no-op while the key buffer
// is blank.
func (m *Model) commitAdd(restart bool) tea.Cmd {
	a := m.adding
	key := strings.TrimSpace(a.key)
	if key == "" {
		return nil
	}
	value := a.value
	m.adding = nil
	return m.beginCommit(key, value, false, "", restart)
}

// commitEdit executes the single-line edit row's rewritten value — a no-op
// when the value is unchanged from its original.
func (m *Model) commitEdit(restart bool) tea.Cmd {
	e := m.editing
	if !e.changed() {
		m.editing = nil
		return nil
	}
	key, value, original := e.key, e.value, e.original
	m.editing = nil
	return m.beginCommit(key, value, true, original, restart)
}

// commitMultiline executes the buffer editor's rewritten value — a no-op
// when unchanged from its original.
func (m *Model) commitMultiline(restart bool) tea.Cmd {
	e := m.multiline
	if !e.changed() {
		m.multiline = nil
		return nil
	}
	key, value, original := e.key, e.value(), strings.Join(e.original, "\n")
	m.multiline = nil
	return m.beginCommit(key, value, true, original, restart)
}

// beginRemove executes a key removal — always TierInline regardless of PROD
// (docs/design README.md §27a inherits 27b's own "removing a key keeps the
// y/N too" policy), never chained with a restart (ctrl-r's own restart
// chaining is described only for the value apply, not a removal).
func (m *Model) beginRemove(row configMapKeyRow) tea.Cmd {
	m.pendingCommit = &configMapPendingCommit{key: row.key, remove: true}
	m.message, m.lastError = "", ""
	return m.actions.Begin(actions.TierInline, tui.TaskAction{
		ID:    "remove-configmap-key-" + m.namespace + "/" + m.name + "/" + row.key,
		Label: fmt.Sprintf("Remove key %s from %s?", row.key, m.name),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindConfigMap), ResourceName: m.name, Namespace: m.namespace,
			Verb: "configmap-data", IsMutating: true,
			ConfigMapKey: row.key, ConfigMapRemove: true,
		},
	})
}

// handleResult applies an add/edit/remove action's outcome — never leaves
// the screen, per docs/design README.md §27a's own "confirm → execute →
// refresh → show result → remain on screen" contract (26a/27b's own
// established shape). On success the ConfigMap (and its consumer list) is
// re-fetched and focus follows the touched key. On failure nothing is
// refetched: a failed add/edit re-opens its own editor with the attempted
// value intact, and the server's error is surfaced via lastError.
func (m *Model) handleResult(msg actions.ResultMsg) tea.Cmd {
	pc := m.pendingCommit
	m.pendingCommit = nil
	if msg.Err != nil {
		m.lastError = msg.Err.Error()
		m.message = ""
		switch {
		case pc == nil || pc.remove:
			// A failed removal has no buffer to restore — the row is still
			// right there, unmoved.
		case pc.isEdit:
			if strings.Contains(pc.value, "\n") || strings.Contains(pc.original, "\n") {
				m.multiline = newMultilineEditState(pc.key, pc.value)
			} else {
				m.editing = &editKeyState{key: pc.key, original: pc.original, value: pc.value, valueCursor: len([]rune(pc.value))}
			}
		default:
			m.adding = &addKeyState{
				key: pc.key, keyCursor: len([]rune(pc.key)),
				value: pc.value, valueCursor: len([]rune(pc.value)),
				onValue: true,
			}
		}
		return nil
	}
	m.lastError = ""
	if pc == nil {
		return nil
	}
	switch {
	case pc.remove:
		m.message = "removed " + pc.key
	case pc.isEdit:
		m.message = "updated " + pc.key
	default:
		m.message = "added " + pc.key
	}
	if pc.restartConsumers && len(pc.consumers) > 0 {
		m.message += fmt.Sprintf(" · restarted %d consumer", len(pc.consumers))
		if len(pc.consumers) != 1 {
			m.message += "s"
		}
	}
	m.focusKey = pc.key
	m.reloadEpoch++
	return m.load()
}

// consumerRefs strips configMapConsumer's display-only refKind down to the
// bare kube.ConfigMapConsumerRef list TaskScope.ConfigMapConsumers carries.
func consumerRefs(consumers []configMapConsumer) []kube.ConfigMapConsumerRef {
	if len(consumers) == 0 {
		return nil
	}
	out := make([]kube.ConfigMapConsumerRef, len(consumers))
	for i, c := range consumers {
		out[i] = c.ConfigMapConsumerRef
	}
	return out
}

// insertInto inserts text into buf at cursor (rune-safe), advancing cursor
// past the inserted text.
func insertInto(buf *string, cursor *int, text string) {
	r := []rune(*buf)
	ins := []rune(text)
	pos := min(max(*cursor, 0), len(r))
	*buf = string(r[:pos]) + string(ins) + string(r[pos:])
	*cursor = pos + len(ins)
}

// insertMultiline pastes text into the buffer editor at the cursor,
// splitting on any newlines the pasted content carries so a multi-line
// paste lands as multiple lines rather than one line with literal '\n's.
func insertMultiline(e *multilineEditState, text string) {
	parts := strings.Split(text, "\n")
	line := []rune(e.lines[e.row])
	before, after := string(line[:e.col]), string(line[e.col:])
	if len(parts) == 1 {
		insertInto(&e.lines[e.row], &e.col, text)
		return
	}
	newLines := make([]string, 0, len(parts))
	newLines = append(newLines, before+parts[0])
	newLines = append(newLines, parts[1:len(parts)-1]...)
	last := parts[len(parts)-1]
	newLines = append(newLines, last+after)
	tail := append([]string{}, e.lines[e.row+1:]...)
	e.lines = append(e.lines[:e.row], newLines...)
	e.lines = append(e.lines, tail...)
	e.row += len(parts) - 1
	e.col = len([]rune(last))
}

// deleteBefore removes the rune immediately before cursor in buf, if any.
func deleteBefore(buf *string, cursor *int) {
	if *cursor <= 0 {
		return
	}
	r := []rune(*buf)
	pos := min(*cursor, len(r))
	*buf = string(r[:pos-1]) + string(r[pos:])
	*cursor = pos - 1
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
