package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ConfirmStyles are the pre-styled spans ConfirmCard composes from — built
// by the caller from Theme.Confirm* tokens, keeping this component
// Theme-agnostic like Card. Border should already carry
// BorderForeground(Theme.ConfirmBorder) + Background(Theme.ConfirmHeaderBg)
// (mvp-plan.md §8b, invariant: "red borders reserved exclusively for
// destructive confirms"). ConfirmHeaderBg is the empty lipgloss.Color in
// both themes (theme.go), so the modal renders on the terminal's own
// background — only the red border itself carries a real color. Rule should
// carry Theme.TextGhost — the same token palette.Styles.Rule and chrome.go's
// band rules use — for the divider ConfirmCard draws above the key row.
type ConfirmStyles struct {
	Border lipgloss.Style
	Title  lipgloss.Style
	Detail lipgloss.Style
	Rule   lipgloss.Style
	Key    lipgloss.Style
	Label  lipgloss.Style
}

// ConfirmCard renders a y/N confirmation prompt centered within width×height:
// title line, an optional detail line (e.g. "3 pods will be evicted"), a
// rule, and a key-hint row (mirroring palette.Model.Render's own divider
// above its key row). This is the minimal inline shape Phase 9's drain
// confirm needs — the full type-the-name PROD modal (mvp-plan.md §8b) is a
// later addition to this same file.
func ConfirmCard(title, detail string, styles ConfirmStyles, width, height int) string {
	box := ConfirmBox(title, detail, styles)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// ConfirmBox renders ConfirmCard's bordered content only, without the
// width×height Place wrapper — for callers that composite it themselves
// (the root shell's quit confirm, layered over the active screen via
// components.Compose rather than centered in its own width×height).
func ConfirmBox(title, detail string, styles ConfirmStyles) string {
	lines := []string{styles.Title.Render(title)}
	if detail != "" {
		lines = append(lines, styles.Detail.Render(detail))
	}
	keyLine := styles.Key.Render("y") + styles.Label.Render(" confirm") + "   " + styles.Key.Render("n") + styles.Label.Render(" cancel")
	lines = append(lines, styles.Rule.Render(strings.Repeat("─", maxLineWidth(lines, keyLine))), keyLine)
	content := strings.Join(lines, "\n")
	return styles.Border.Border(lipgloss.RoundedBorder()).Padding(1, 3).Render(content)
}

// TypeModalStyles are the pre-styled spans TypeNameModal composes from —
// built by the caller from Theme.ConfirmBorder/ConfirmHeaderBg (border/
// header — "the app's only red-bordered surface") and Theme.ProdBorder/
// ProdText (the PROD CONTEXT tag, the same tokens 7a's context palette
// already uses for the same meaning). Kept Theme-agnostic like ConfirmCard.
// Rule carries Theme.TextGhost, same as ConfirmStyles.Rule.
type TypeModalStyles struct {
	Border   lipgloss.Style
	Title    lipgloss.Style
	ProdTag  lipgloss.Style
	Owner    lipgloss.Style
	Detail   lipgloss.Style
	Rule     lipgloss.Style
	Input    lipgloss.Style
	Progress lipgloss.Style
	Key      lipgloss.Style
	Label    lipgloss.Style
}

// TypeNameModal renders 8b's type-the-name destructive confirm (docs/design
// README.md §8b): title (+ a right-aligned PROD CONTEXT tag when prod), an
// optional owner line ("Deployment/x — will be recreated"), a detail line
// (grace period / force-delete hint), the type-ahead prompt with a trailing
// cursor and "N/M" progress count, and the key row. ↵ only executes once
// typed == target — screens gate that via actions.Controller.Confirm, this
// component is purely presentational.
func TypeNameModal(title, ownerLine, detailLine, target, typed string, prod bool, styles TypeModalStyles, width, height int) string {
	titleLine := styles.Title.Render(title)

	body := []string{}
	if ownerLine != "" {
		body = append(body, styles.Owner.Render(ownerLine))
	}
	if detailLine != "" {
		body = append(body, styles.Detail.Render(detailLine))
	}
	body = append(body, "", styles.Detail.Render("type \""+target+"\" to confirm"))
	body = append(body, styles.Input.Render(typed+"█")+"  "+styles.Progress.Render(fmt.Sprintf("%d/%d", len(typed), len(target))))
	keyLine := styles.Key.Render("↵") + styles.Label.Render(" delete (when name matches)") + "   " +
		styles.Key.Render("esc") + styles.Label.Render(" cancel")

	if prod {
		prodTag := styles.ProdTag.Render("PROD CONTEXT")
		titleLine = padBetween2(titleLine, prodTag, max(maxLineWidth(body, keyLine), lipgloss.Width(titleLine)+lipgloss.Width(prodTag)+1))
	}

	lines := append([]string{titleLine}, body...)
	lines = append(lines, styles.Rule.Render(strings.Repeat("─", maxLineWidth(lines, keyLine))), keyLine)

	content := strings.Join(lines, "\n")
	box := styles.Border.Border(lipgloss.RoundedBorder()).Padding(1, 3).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// TypeCountModal renders 20a's bulk PROD confirm — the count-based sibling
// of TypeNameModal: 8b's PROD delete escalation "becomes type-the-count"
// when the pending action targets a marked set rather than one resource
// (docs/design README.md §20a: "type 3 to confirm"). title (+ PROD CONTEXT
// tag), objectsLine listing every marked object, an optional detail line
// (grace period), the type-ahead prompt against the count's digit string,
// and the key row. ↵ only executes once typed == strconv of count — screens
// gate that themselves (browse's updateBulkDeleteKey), this component is
// purely presentational, same contract as TypeNameModal.
func TypeCountModal(title, objectsLine, detailLine string, count int, typed string, prod bool, styles TypeModalStyles, width, height int) string {
	target := fmt.Sprintf("%d", count)
	titleLine := styles.Title.Render(title)

	body := []string{}
	if objectsLine != "" {
		body = append(body, styles.Owner.Render(objectsLine))
	}
	if detailLine != "" {
		body = append(body, styles.Detail.Render(detailLine))
	}
	body = append(body, "", styles.Detail.Render("type \""+target+"\" to confirm"))
	body = append(body, styles.Input.Render(typed+"█")+"  "+styles.Progress.Render(fmt.Sprintf("%d/%d", len(typed), len(target))))
	keyLine := styles.Key.Render("↵") + styles.Label.Render(" delete (when count matches)") + "   " +
		styles.Key.Render("esc") + styles.Label.Render(" cancel")

	if prod {
		prodTag := styles.ProdTag.Render("PROD CONTEXT")
		titleLine = padBetween2(titleLine, prodTag, max(maxLineWidth(body, keyLine), lipgloss.Width(titleLine)+lipgloss.Width(prodTag)+1))
	}

	lines := append([]string{titleLine}, body...)
	lines = append(lines, styles.Rule.Render(strings.Repeat("─", maxLineWidth(lines, keyLine))), keyLine)

	content := strings.Join(lines, "\n")
	box := styles.Border.Border(lipgloss.RoundedBorder()).Padding(1, 3).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// padBetween2 right-pads left so right lands at width — the title row's
// PROD CONTEXT tag alignment. Named distinctly from any per-task padBetween
// helper since this file is shared across every screen that renders a modal.
func padBetween2(left, right string, width int) string {
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

// maxLineWidth measures a rule's target width: the widest of lines and any
// extra strings (e.g. the key row rendered separately from lines), all
// already-styled — lipgloss.Width ignores the ANSI.
func maxLineWidth(lines []string, extra ...string) int {
	w := 0
	for _, l := range append(append([]string{}, lines...), extra...) {
		if lw := lipgloss.Width(l); lw > w {
			w = lw
		}
	}
	return w
}
