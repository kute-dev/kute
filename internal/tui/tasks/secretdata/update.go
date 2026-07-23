package secretdata

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
		if msg.Kind == kube.KindSecret {
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
		// docs/design README.md §27b: "ctrl-v paste (never echoed to
		// scrollback)" — the app renders full-alt-screen (tui.Model.View
		// sets AltScreen), so nothing a real terminal's bracketed-paste
		// delivers here ever touches scrollback regardless; this just
		// routes the pasted text into whichever add/edit buffer has focus.
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
		m.feedback = fmt.Sprintf("secret %s/%s not found", m.namespace, m.name)
		return m, nil
	}
	m.secretType = msg.secretType
	m.keys = msg.keys
	m.state = tui.TaskStateReady
	m.feedback = ""
	switch {
	case m.focusKey != "":
		if idx, ok := indexOfSecretKey(m.keys, m.focusKey); ok {
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
	if msg.String() != "esc" {
		// A leftover "added SMTP_PASSWORD"/error line from the last commit
		// only answers "what just happened" — stale the moment the user
		// does anything else, same rule 26a's meta.go uses.
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
				m.editing = &editKeyState{key: row.key, original: row.value, value: row.value, valueCursor: len([]rune(row.value))}
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
// showing — a text-entry context, so every printable character (including
// 'x') inserts literally into whichever buffer has focus; ctrl-x toggles
// the value's mask (docs/design README.md §27b: "ctrl-x re-mask input" —
// a chorded key so plain 'x' stays available to type, the same reasoning
// meta.go's own ctrl-d removal chord uses rather than a bare letter).
// tab/shift+tab move focus between the key and value buffers, the same
// two-buffer shape meta.go's own add sub-flow uses.
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
		return m, m.commitAdd()
	case "ctrl+x":
		a.masked = !a.masked
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

// updateEditKey routes keys while '↵'s decode-then-edit row is showing —
// a single-buffer text-entry context (the key itself isn't editable here),
// so every printable character inserts literally; ctrl-x toggles the mask,
// same as the add row. esc reverts to the original decoded value without
// applying anything.
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
		return m, m.commitEdit()
	case "ctrl+x":
		e.masked = !e.masked
	default:
		if msg.Text != "" {
			insertInto(&e.value, &e.valueCursor, msg.Text)
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

// commitAdd executes the add row's key=value through actions.Controller —
// TierNone (applies immediately) outside PROD, TierInline (inline y/N)
// in PROD (docs/design README.md §27b, verbs.TierForAddSecretKey). A no-op
// while the key buffer is blank.
func (m *Model) commitAdd() tea.Cmd {
	a := m.adding
	key := strings.TrimSpace(a.key)
	if key == "" {
		return nil
	}
	value := a.value
	m.adding = nil
	m.pendingCommit = &secretPendingCommit{key: key, value: value}
	m.message, m.lastError = "", ""
	tier := verbs.TierForAddSecretKey(m.isProd())
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    "add-secret-key-" + m.namespace + "/" + m.name + "/" + key,
		Label: fmt.Sprintf("Add key %s to %s?", key, m.name),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindSecret), ResourceName: m.name, Namespace: m.namespace,
			Verb: "secret-data", IsMutating: true,
			SecretKey: key, SecretValue: value,
		},
	})
}

// commitEdit executes the edit row's rewritten value through
// actions.Controller — same TierNone-outside-PROD/TierInline-in-PROD policy
// as commitAdd (verbs.TierForAddSecretKey; both go through the same
// PatchSecretData call, so the same tiering applies). A no-op when the
// value is unchanged from its original decoded value — nothing to apply.
func (m *Model) commitEdit() tea.Cmd {
	e := m.editing
	if !e.changed() {
		m.editing = nil
		return nil
	}
	key, value, original := e.key, e.value, e.original
	m.editing = nil
	m.pendingCommit = &secretPendingCommit{key: key, value: value, isEdit: true, original: original}
	m.message, m.lastError = "", ""
	tier := verbs.TierForAddSecretKey(m.isProd())
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    "edit-secret-key-" + m.namespace + "/" + m.name + "/" + key,
		Label: fmt.Sprintf("Update key %s on %s?", key, m.name),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindSecret), ResourceName: m.name, Namespace: m.namespace,
			Verb: "secret-data", IsMutating: true,
			SecretKey: key, SecretValue: value,
		},
	})
}

// beginRemove executes a key removal through actions.Controller — always
// TierInline regardless of PROD (docs/design README.md §27b: "removing a
// key keeps the y/N too"), never escalated further to a type-the-name
// modal, the same policy 26a's meta.go removal uses.
func (m *Model) beginRemove(row secretKeyRow) tea.Cmd {
	m.pendingCommit = &secretPendingCommit{key: row.key, remove: true}
	m.message, m.lastError = "", ""
	return m.actions.Begin(actions.TierInline, tui.TaskAction{
		ID:    "remove-secret-key-" + m.namespace + "/" + m.name + "/" + row.key,
		Label: fmt.Sprintf("Remove key %s from %s?", row.key, m.name),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindSecret), ResourceName: m.name, Namespace: m.namespace,
			Verb: "secret-data", IsMutating: true,
			SecretKey: row.key, SecretRemove: true,
		},
	})
}

// handleResult applies an add/edit/remove action's outcome — update.go's
// actions.ResultMsg case calls this instead of ever leaving the screen,
// per docs/design README.md §27b's own contract: "confirm → execute →
// refresh → show result → remain on screen." On success the Secret is
// re-fetched (never an optimistic local patch) and focus follows the
// touched key — the same row again after an add or edit, or the nearest
// remaining row after a removal (applyLoaded's own fallback once
// indexOfSecretKey can't find it, since it's gone). On failure nothing is
// refetched: a failed add/edit re-opens its own row with the attempted
// value intact, and the server's error is surfaced via lastError (view.go's
// will-run strip) — never the value, per this screen's own no-leak rule.
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
			m.editing = &editKeyState{key: pc.key, original: pc.original, value: pc.value, valueCursor: len([]rune(pc.value))}
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
	m.focusKey = pc.key
	m.reloadEpoch++
	return m.load()
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
