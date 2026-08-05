// ctrl-d delete machinery for 8b (docs/design README.md §8b, mvp-plan.md
// §Phase 5): any row, any kind (verbs.Delete.Kinds is nil — "all kinds").
// Kept in its own file, browse's per-concern split convention (like
// nodes.go/sort.go/grouping.go/metrics.go) rather than sprinkled through
// model.go/view.go/update.go.
package browse

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/components"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// isDeleteVerb reports whether verb is the delete family 8b's type-the-name
// modal handles — the narrower set typeNameConfirmModal needs to tell apart
// from 9a's rollout-restart, since only the delete family carries an owner/
// grace-period detail.
func isDeleteVerb(verb string) bool {
	return verb == "delete" || verb == "force-delete"
}

// requiresTypeNameConfirm reports whether verb's TierModal confirmation
// needs the full type-the-name modal (components.TypeNameModal) rather than
// the plain y/N ConfirmCard — the delete family plus 9a's rollout-restart
// (docs/design README.md §9a/§419: "delete and rollout restart are both
// tiered — inline y/N in non-prod, type-the-name modal in PROD contexts").
// Everything else that reaches TierModal (Drain) keeps rendering through
// the existing minimal ConfirmCard. Mirrors actions.requiresTypedName's verb
// set for the subset of verbs browse itself renders — rollout-undo is
// timeline's screen, not browse's, so it never reaches this gate.
func requiresTypeNameConfirm(verb string) bool {
	return isDeleteVerb(verb) || verb == "rollout-restart"
}

// typingConfirmName reports whether the type-the-name modal is the active
// confirm surface — i.e. whether there's a text buffer on screen at all.
// updateConfirmKey routes keys by it and pasteTarget routes pastes by it, so
// the two can't disagree about where typed input is going.
func (m Model) typingConfirmName() bool {
	pending := m.actions.Pending()
	return m.actions.Tier() == actions.TierModal && pending != nil && requiresTypeNameConfirm(pending.Scope.Verb)
}

// isProd reports whether the active session's current context is tagged
// prod in ~/.config/kute/config.yaml — the same source 7a's context
// palette PROD tag reads (internal/tui/context.go).
func (m Model) isProd() bool {
	if m.session == nil {
		return false
	}
	return m.session.Config.IsProd(m.session.Location.Context)
}

// deleteWillRunLine renders 8b's inline (non-PROD) confirm line — the exact
// `kubectl delete` command that will run, replacing the generic
// actions.Controller.Prompt() text (which only duplicated the y/n hints
// already in the keybar's Groups). Kept prefix-free like rollbackPrompt:
// insetChromeLine (tui/chrome.go) drops the whole RightNote rather than
// truncating it when the keybar line overflows width.
func deleteWillRunLine(scope tui.TaskScope) string {
	return kube.DeleteCommandString(kube.ResourceKind(scope.ResourceKind), scope.Namespace, []string{scope.ResourceName})
}

// forceDeleteWillRunLine is deleteWillRunLine's counterpart for the inline
// confirm's force-delete sub-state (ctrl-k, actions.Controller.ForceArmed) —
// the same command plus the --grace-period=0 --force flags
// DeleteResourceForced actually passes to the API, so the operator sees
// exactly what the next "y" runs.
func forceDeleteWillRunLine(scope tui.TaskScope) string {
	return kube.ForceDeleteCommandString(kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName)
}

// beginDelete confirms deleting row — inline y/N in non-prod contexts, the
// full type-the-name modal in PROD (verbs.TierFor). Owner is only known for
// the Pod kind (via m.pods, the fuller kube.Pod projection Row doesn't
// carry) — every other kind's modal simply omits the "will be recreated"
// line. Deleting a CustomResourceDefinition (14b) always gets the
// type-the-name modal, even outside PROD — it deletes every instance of
// that kind too, not just the one row.
func (m *Model) beginDelete(row resources.Row) tea.Cmd {
	var owner string
	var gracePeriod *int64
	if m.kind == kube.KindPod {
		if pod, ok := m.pods[row.Name]; ok {
			owner = pod.Owner
			gracePeriod = &pod.GracePeriodSeconds
		}
	}
	tier := verbs.TierFor(verbs.Delete, m.isProd())
	if m.kind == kube.KindCustomResourceDefinition {
		tier = actions.TierModal
	}
	return m.actions.Begin(tier, tui.TaskAction{
		ID:                 "delete-" + string(m.kind) + "-" + row.Namespace + "/" + row.Name,
		Label:              fmt.Sprintf("Delete %s %s?", singularDisplay(m.desc.Display), row.Name),
		Owner:              owner,
		GracePeriodSeconds: gracePeriod,
		Scope: tui.TaskScope{
			ResourceKind: string(m.kind),
			ResourceName: row.Name,
			Namespace:    row.Namespace,
			Verb:         "delete",
			IsMutating:   true,
		},
	})
}

// typeNameConfirmModal renders the type-the-name modal for a pending
// delete/force-delete or rollout-restart — the verbs requiresTypeNameConfirm
// routes here (every other TierModal verb, i.e. Drain, keeps rendering
// through nodes.go's existing confirmBody/ConfirmCard, untouched by this
// file). The delete family gets 8b's owner/grace-period detail and its ctrl-k
// force-delete hint; rollout-restart (9a) has neither concept, so it gets the
// same "will run: kubectl rollout restart …" line the non-prod inline
// confirm's keybar already shows.
func (m Model) typeNameConfirmModal(width, height int) string {
	theme := m.Theme()
	title := "Confirm"
	target := ""
	detail := ""
	actionVerb := "delete"
	var ownerLine string
	if pending := m.actions.Pending(); pending != nil {
		title = "✕ " + pending.Label
		target = pending.Scope.ResourceName
		if pending.Scope.Verb == "rollout-restart" {
			actionVerb = "restart"
			detail = rolloutRestartWillRunLine(pending.Scope)
		} else {
			if pending.Owner != "" {
				ownerLine = pending.Owner + " — will be recreated"
			}
			if pending.Scope.Verb == "force-delete" {
				detail = "grace period 0 — force delete, immediate"
			} else {
				// docs/design README.md §8b: the concrete figure (e.g. "30s"),
				// not a generic "default grace period applies" — falls back to
				// the generic text when the caller didn't resolve one (every
				// non-Pod kind).
				if pending.GracePeriodSeconds != nil {
					detail = fmt.Sprintf("grace period %ds applies", *pending.GracePeriodSeconds)
				} else {
					detail = "default grace period applies"
				}
				if pending.Scope.ResourceKind == string(kube.KindPod) {
					detail += " · ctrl-k force delete (immediate)"
				}
			}
		}
	}

	styles := components.TypeModalStyles{
		Border:   lipgloss.NewStyle().BorderForeground(theme.ConfirmBorder).Background(theme.ConfirmHeaderBg),
		Title:    lipgloss.NewStyle().Foreground(theme.Bad).Bold(true).Background(theme.ConfirmHeaderBg),
		ProdTag:  lipgloss.NewStyle().Foreground(theme.ProdText).Bold(true).Background(theme.ConfirmHeaderBg),
		Owner:    lipgloss.NewStyle().Foreground(theme.Good).Background(theme.ConfirmHeaderBg),
		Detail:   lipgloss.NewStyle().Foreground(theme.TextSecondary).Background(theme.ConfirmHeaderBg),
		Rule:     lipgloss.NewStyle().Foreground(theme.TextGhost).Background(theme.ConfirmHeaderBg),
		Input:    lipgloss.NewStyle().Foreground(theme.Text).Background(theme.ConfirmHeaderBg),
		Progress: lipgloss.NewStyle().Foreground(theme.TextFaint).Background(theme.ConfirmHeaderBg),
		Key:      lipgloss.NewStyle().Foreground(theme.Bad).Background(theme.ConfirmHeaderBg),
		Label:    lipgloss.NewStyle().Foreground(theme.TextDim).Background(theme.ConfirmHeaderBg),
	}
	return components.TypeNameModal(title, ownerLine, detail, target, m.actions.TypedName(), actionVerb, m.isProd(), styles, width, height)
}
