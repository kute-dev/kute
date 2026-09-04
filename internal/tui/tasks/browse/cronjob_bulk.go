// §36c's marked-set suspend rendering (0.8.0 plan §3 Phase 5 tasks 8/10):
// bulkCronJobSuspendConfirmModal is the PROD type-the-count surface a
// marked-set "cronjob-suspend" TierModal confirm renders instead of
// delete.go's single-target typeNameConfirmModal — reusing 20a's own
// type-the-count grammar (components.TypeCountModal) but sourced from
// actions.Controller's structured BulkTargets payload rather than a
// bespoke local gate like pendingBulkDelete, so the confirm→execute path
// stays the shared executeBulk/BulkResultMsg machinery every bulk-capable
// verb goes through. Kept in its own file: jobs.go/cronjob_actions.go both
// already carry real weight, and this is a rendering + key-routing
// concern, not a staging one.
package browse

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

// bulkCronJobSuspendWillRunLine is the TierInline (non-PROD) keybar note for
// a marked-set suspend — names every target rather than a single kubectl
// invocation, since one command can't represent N independent patches
// (mirrors bulkDeleteWillRunLine's own cross-namespace fallback text).
func bulkCronJobSuspendWillRunLine(scope tui.TaskScope) string {
	names := make([]string, len(scope.BulkTargets))
	for i, t := range scope.BulkTargets {
		names[i] = t.Namespace + "/" + t.ResourceName
	}
	return fmt.Sprintf("will suspend %d cronjobs: %s — running jobs unaffected", len(scope.BulkTargets), strings.Join(names, ", "))
}

// bulkCronJobSuspendConfirmModal renders the PROD type-the-count modal for a
// marked-set suspend: title, every marked object, a note that running Jobs
// are unaffected, and the type-ahead prompt against the count — the same
// shape bulkDeleteConfirmModal (bulk.go) uses for delete, sourced from
// m.actions.Pending() instead of a local pendingBulkDelete.
func (m Model) bulkCronJobSuspendConfirmModal(width, height int) string {
	theme := m.Theme()
	pending := m.actions.Pending()
	if pending == nil {
		return components.CenterLines([]string{"Confirm"}, width, height)
	}
	targets := pending.Scope.BulkTargets
	count := len(targets)
	title := fmt.Sprintf("⏸ Suspend %d cronjobs?", count)

	_, uniform := bulkTargetNamespace(targets)
	objectsLine := wrapLabels(bulkTargetLabels(targets, uniform), 56)

	styles := components.TypeModalStyles{
		Border:    lipgloss.NewStyle().BorderForeground(theme.ConfirmBorder).Background(theme.ConfirmHeaderBg),
		Title:     lipgloss.NewStyle().Foreground(theme.Bad).Bold(true).Background(theme.ConfirmHeaderBg),
		ProdTag:   lipgloss.NewStyle().Foreground(theme.ProdText).Bold(true).Background(theme.ConfirmHeaderBg),
		Owner:     lipgloss.NewStyle().Foreground(theme.TextSecondary).Background(theme.ConfirmHeaderBg),
		Detail:    lipgloss.NewStyle().Foreground(theme.TextSecondary).Background(theme.ConfirmHeaderBg),
		Rule:      lipgloss.NewStyle().Foreground(theme.TextGhost).Background(theme.ConfirmHeaderBg),
		Input:     lipgloss.NewStyle().Foreground(theme.Text).Background(theme.ConfirmHeaderBg),
		Selection: lipgloss.NewStyle().Foreground(theme.Bg).Background(theme.Accent),
		Progress:  lipgloss.NewStyle().Foreground(theme.TextFaint).Background(theme.ConfirmHeaderBg),
		Key:       lipgloss.NewStyle().Foreground(theme.Bad).Background(theme.ConfirmHeaderBg),
		Label:     lipgloss.NewStyle().Foreground(theme.TextDim).Background(theme.ConfirmHeaderBg),
	}
	return components.TypeCountModal(title, objectsLine, "running jobs are unaffected — this only stops future scheduling", count, m.actions.TypedInput(), m.isProd(), styles, width, height)
}

// bulkTargetNamespace/bulkTargetLabels mirror bulkNamespace/bulkObjectLabels
// (bulk.go) for a []tui.BulkTarget rather than a []resources.Row — the two
// bulk grammars (delete's local pendingBulkDelete, suspend's
// actions.Controller BulkTargets) carry namespace-qualified targets in
// different shapes, so the formatting helpers aren't directly shared.
func bulkTargetNamespace(targets []tui.BulkTarget) (string, bool) {
	if len(targets) == 0 {
		return "", true
	}
	ns := targets[0].Namespace
	for _, t := range targets[1:] {
		if t.Namespace != ns {
			return "", false
		}
	}
	return ns, true
}

func bulkTargetLabels(targets []tui.BulkTarget, uniformNamespace bool) []string {
	labels := make([]string, len(targets))
	for i, t := range targets {
		if uniformNamespace {
			labels[i] = t.ResourceName
		} else {
			labels[i] = t.Namespace + "/" + t.ResourceName
		}
	}
	return labels
}

// bulkCronJobSuspendPasteTarget wraps the controller's own type-ahead buffer
// with digit-only filtering (tui.PasteDigits) — the count grammar's own
// rule, mirrored from bulk.go's pendingBulkDelete paste path, that
// actions.Controller.PasteTarget can't apply on its own since it also backs
// the unfiltered type-the-name grammar every other TierModal verb uses.
func (m Model) bulkCronJobSuspendPasteTarget() tui.PasteTarget {
	return tui.PasteDigits(m.actions.PasteTarget())
}

// pendingBulkCronJobSuspend reports whether the active confirm is a
// marked-set "cronjob-suspend" — the one condition that routes Body()'s
// TierModal branch, updateModalConfirmKey's enter/typing, and pasteTarget to
// this file's bulk count grammar instead of delete.go's type-the-name one.
func (m Model) pendingBulkCronJobSuspend() bool {
	pending := m.actions.Pending()
	return pending != nil && pending.Scope.Verb == "cronjob-suspend" && len(pending.Scope.BulkTargets) > 0
}

// bulkCronJobSuspendCountMatches reports whether the controller's typed
// buffer currently equals the marked count — updateModalConfirmKey's own
// enter gate for the bulk grammar (mirrors updateBulkDeleteKey's identical
// check against pendingBulkDelete's local buffer).
func (m Model) bulkCronJobSuspendCountMatches() bool {
	pending := m.actions.Pending()
	if pending == nil {
		return false
	}
	return m.actions.TypedName() == strconv.Itoa(len(pending.Scope.BulkTargets))
}

// bulkCronJobSuspendKeyIsDigit reports whether msg's Text is safe to forward
// into the count buffer — digits only, matching the field's count semantics
// (mirrors updateBulkDeleteKey's identical filter). Non-text control keys
// (backspace, arrows, Home/End) carry no Text and are always allowed
// through.
func bulkCronJobSuspendKeyIsDigit(msg tea.KeyPressMsg) bool {
	for _, r := range msg.Text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
