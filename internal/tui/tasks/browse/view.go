package browse

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/components"
)

func (m Model) View() tea.View { return tea.NewView(m.Render()) }

func (m Model) Render() string { return tui.Frame(m.width, m.height, m) }

func (m Model) Theme() tui.Theme {
	if m.session != nil {
		return m.session.Theme
	}
	return tui.Dark()
}

func (m Model) Header() tui.HeaderState {
	theme := m.Theme()
	accent := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
	ghost := lipgloss.NewStyle().Foreground(theme.TextGhost)
	ghost2 := lipgloss.NewStyle().Foreground(theme.TextGhost2)
	text := lipgloss.NewStyle().Foreground(theme.Text).Bold(true)

	ctxName := "cluster unavailable"
	if m.session != nil && m.session.Location.Context != "" {
		ctxName = m.session.Location.Context
	}

	crumbs := []tui.Crumb{
		{Text: "kute", Style: accent},
		{Text: " │ ", Style: ghost2},
		{Text: ctxName, Style: dim},
	}
	if !m.desc.ClusterScoped {
		nsText, nsStyle := m.namespace, lipgloss.NewStyle().Foreground(theme.Accent)
		if m.grouped() {
			// 6b: scope is never ambiguous — the blue ALL-NS token, not the
			// namespace's usual purple accent (docs/design README.md §6b).
			nsText, nsStyle = tui.GlyphAllNS+" all namespaces", lipgloss.NewStyle().Foreground(theme.Info)
		}
		crumbs = append(crumbs,
			tui.Crumb{Text: " › ", Style: ghost},
			tui.Crumb{Text: nsText, Style: nsStyle},
		)
	}
	if m.originName != "" {
		// esc-back-to-origin (deployments.go's openDeploymentPods, helm.go's
		// openReleaseObjects): names the owning row between the namespace
		// and kind segments, e.g. "… › nva-stage › my-deploy › Pods".
		crumbs = append(crumbs,
			tui.Crumb{Text: " › ", Style: ghost},
			tui.Crumb{Text: m.originName, Style: dim},
		)
	}
	crumbs = append(crumbs,
		tui.Crumb{Text: " › ", Style: ghost},
		tui.Crumb{Text: m.desc.Display, Style: text},
	)
	if m.desc.Custom && m.desc.APIGroup != "" && m.desc.APIVersion != "" {
		// docs/design README.md §14a: "Certificates + cert-manager.io/v1" —
		// the CRD's own API group/version, dim, right after the kind name.
		crumbs = append(crumbs, tui.Crumb{Text: " " + m.desc.APIGroup + "/" + m.desc.APIVersion, Style: faint})
	}
	switch {
	case m.kind == kube.KindForward:
		// docs/design README.md §13c: "all namespaces tag (forwards are
		// global, never namespace-filtered)" — Forwards reuses Nodes'
		// ClusterScoped semantics (no per-namespace switching/refetch at
		// all) but is namespaced data viewed globally, not truly
		// cluster-scoped like Nodes, so the breadcrumb tag reads
		// differently — matching 6b's own "all namespaces" blue wording.
		crumbs = append(crumbs, tui.Crumb{Text: "  " + tui.GlyphAllNS + " all namespaces", Style: lipgloss.NewStyle().Foreground(theme.Info)})
	case m.desc.ClusterScoped:
		// 11a: "… cluster › Nodes" + a small cluster-scoped tag — the
		// namespace segment is already dropped above.
		crumbs = append(crumbs, tui.Crumb{Text: "  cluster-scoped", Style: faint})
	}
	crumbs = append(crumbs, tui.Crumb{Text: " (g to jump)", Style: faint})

	if m.state == tui.TaskStateLoading {
		// 15a: the header's right side is a counting timer instead of the
		// usual sync/forward/conn badges — "what" is loading and for how
		// long, with no fake progress bar (docs/design README.md §15a).
		elapsed := max(m.now.Sub(m.loadStartedAt), 0)
		return tui.HeaderState{
			Crumbs: crumbs,
			Conn: tui.ConnBadge{
				Text:  fmt.Sprintf("%s loading %s · %.1fs", tui.GlyphPending, lowerDisplay(m.desc.Display), elapsed.Seconds()),
				Style: lipgloss.NewStyle().Foreground(theme.Warn),
			},
		}
	}

	syncNote := ""
	if m.pollsMetrics() {
		syncNote = "sync 2s"
	}

	return tui.HeaderState{
		Crumbs:      crumbs,
		SyncNote:    syncNote,
		UpdateChip:  tui.BuildUpdateChip(theme, m.session),
		ForwardChip: tui.BuildForwardChip(theme, m.session.ForwardSummary()),
		Conn:        tui.LiveConnBadge(theme, m.conn, tui.GlyphRunning+" connected"),
	}
}

// stripLineCount is how many Strips lines the current state renders — kept
// in sync with Strips itself so selection.go's tableDataRows can budget the
// table viewport correctly.
func (m Model) stripLineCount() int {
	switch m.state {
	case tui.TaskStateEmpty, tui.TaskStateLoading:
		return 1
	case tui.TaskStateReady:
		n := 1
		if m.offline() {
			n = 2 // banner + stale strip, replacing the health strip
		}
		if m.filterActive {
			n++
		}
		return n
	default:
		return 0
	}
}

// Strips shows the table's column header line in the empty state (10c: "the
// table column header still renders — the app is fine"), the per-status
// health strip once ready (or, offline, the 4a reconnect banner + stale
// snapshot strip in its place), and — while filtering — an extra line with
// the live query, matched/total, and a "hidden by filter" notice.
func (m Model) Strips(width int) []string {
	theme := m.Theme()
	switch m.state {
	case tui.TaskStateEmpty:
		return []string{m.columnHeaderLine(theme, width)}
	case tui.TaskStateLoading:
		return []string{m.loadingStripLine(theme, width)}
	case tui.TaskStateReady:
		var lines []string
		if m.offline() {
			lines = []string{m.offlineBannerLine(theme, width), m.errorBannerRuleLine(theme, width), m.staleStripLine(theme, width)}
		} else {
			lines = []string{m.healthStripLine(theme, width)}
		}
		if m.filterActive {
			lines = append(lines, m.filterStripLine(theme, width))
		}
		return lines
	default:
		return nil
	}
}

// offlineBannerLine is 4a's reconnect banner: verbatim watch/ping error,
// attempt/backoff countdown, and the r/c exits (docs/design README.md §4a:
// "bg #2a1518, border-bottom #4a2228"). Unlike every other strip line (the
// transparent, terminal-background convention documented on paletteStyles),
// this one carries a real fill — every span below bakes theme.ErrBannerBg
// in explicitly, including the pad/gap fills, since an outer wrap can't do
// it: each inner Render's own ANSI reset would cancel it before the line
// finishes.
func (m Model) offlineBannerLine(theme tui.Theme, width int) string {
	fill := lipgloss.NewStyle().Background(theme.ErrBannerBg)
	warn := fill.Foreground(theme.Bad)
	text := fill.Foreground(theme.BadText)
	dim := fill.Foreground(theme.BadMuted)
	key := fill.Foreground(theme.BadSoft)

	errText := m.conn.Err
	if errText == "" {
		errText = "connection lost"
	}
	left := warn.Render(tui.GlyphWarning) + fill.Render(" ") + text.Render(errText)

	next := max(m.conn.NextRetryAt.Sub(m.now), 0)
	right := dim.Render(fmt.Sprintf("retry %d · next in %ds", m.conn.Attempt, int(next.Round(time.Second).Seconds()))) +
		fill.Render("   ") + key.Render("r") + fill.Render(" ") + dim.Render("retry now") +
		fill.Render("   ") + key.Render("c") + fill.Render(" ") + dim.Render("switch context")

	return insetStripLineFill(padBetweenFill(left, right, stripInnerWidth(width), fill), width, fill)
}

// errorBannerRuleLine draws 4a's "border-bottom #4a2228" under the offline
// banner — the one strip divider that isn't Frame's own inter-band rule,
// since offlineBannerLine is the one strip line with a real background fill
// the others don't carry.
func (m Model) errorBannerRuleLine(theme tui.Theme, width int) string {
	return lipgloss.NewStyle().Foreground(theme.ErrBannerBorder).Render(strings.Repeat("─", width))
}

// staleStripLine is 4a's strip replacing the health strip: the age of the
// kept snapshot, and a note that counts are frozen (docs/design README.md
// §4a: "counts frozen · 36 pods").
func (m Model) staleStripLine(theme tui.Theme, width int) string {
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	dim := lipgloss.NewStyle().Foreground(theme.TextFaint)

	age := max(m.now.Sub(m.fetchedAt), 0)
	left := warn.Render(fmt.Sprintf("%s showing snapshot from %s · %ds old",
		tui.GlyphStale, m.fetchedAt.Format("15:04:05"), int(age.Round(time.Second).Seconds())))
	right := dim.Render(fmt.Sprintf("counts frozen · %d %s", len(m.rows), lowerDisplay(m.desc.Display)))

	return insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)
}

// browseColumns is resources.Columns with the Restarts title swapped for
// the ↺ glyph (docs/design §2a). The mapping lives here rather than in
// resources because glyph choice is tui/glyphs.go's choke point and
// resources can't import tui (cycle via Session).
func browseColumns(desc resources.Descriptor) []components.Column {
	cols := resources.Columns(desc)
	for i := range cols {
		if cols[i].Title == "Restarts" {
			cols[i].Title = tui.GlyphRestarts
		}
	}
	return cols
}

func (m Model) columnHeaderLine(theme tui.Theme, width int) string {
	cols := browseColumns(m.desc)
	if len(cols) == 0 {
		return ""
	}
	t := components.Table{Columns: cols, HeaderStyle: lipgloss.NewStyle().Foreground(theme.TextFaint)}
	return t.HeaderLine(width)
}

// healthStripLine renders the per-status glyph+count segments (docs/design
// §2a: "● 32 running · ◐ 2 pending · ✕ 1 crashloop · ○ 1 completed") in
// OK/Warn/Fail/Neutral order, skipping zero counts, plus a right-aligned
// total.
func (m Model) healthStripLine(theme tui.Theme, width int) string {
	counts := m.desc.Health(m.rows)
	if m.desc.Custom && len(m.rows) > 0 && counts.OK+counts.Warn+counts.Fail == 0 {
		// docs/design README.md §14a: a CRD kind whose instances carry no
		// Ready/Available condition at all gets no fake health — the strip
		// drops the per-status counts and says so instead.
		faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
		note := faint.Render("no status semantics · NAME + AGE only")
		right := lipgloss.NewStyle().Foreground(theme.TextDim).Render(fmt.Sprintf("%d %s", len(m.rows), lowerDisplay(m.desc.Display)))
		return insetStripLine(padBetween(note, right, stripInnerWidth(width)), width)
	}
	segments := []struct {
		class resources.StatusClass
		n     int
	}{
		{resources.StatusOK, counts.OK},
		{resources.StatusWarn, counts.Warn},
		{resources.StatusFail, counts.Fail},
		{resources.StatusNeutral, counts.Neutral},
	}
	numStyle := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	labelStyle := lipgloss.NewStyle().Foreground(theme.TextDim)

	var parts []string
	if n := len(m.marks); n > 0 {
		// 20a: "strip's first slot becomes ▪ N marked" — takes over the
		// leading segment rather than fighting the health counts for room.
		markStyle := lipgloss.NewStyle().Foreground(theme.Accent)
		parts = append(parts, markStyle.Render(tui.GlyphMarked)+" "+numStyle.Render(fmt.Sprintf("%d", n))+" "+labelStyle.Render("marked"))
	}
	for _, seg := range segments {
		if seg.n == 0 {
			continue
		}
		glyphStyle := lipgloss.NewStyle().Foreground(glyphColor(theme, seg.class))
		label := m.desc.HealthLabel(seg.class)
		glyph := defaultGlyphFor(seg.class)
		if m.kind == kube.KindNode && seg.class == resources.StatusNeutral {
			// 11a: cordoned nodes render ◈, not the generic ○ "completed"
			// glyph every other kind's Neutral class falls back to.
			glyph = tui.GlyphCordoned
		}
		if m.kind == kube.KindHelmRelease && seg.class == resources.StatusWarn {
			// 18a's own strip example renders pending-upgrade as ◌, not the
			// generic ◐ pending glyph every other kind's Warn class uses.
			glyph = tui.GlyphProbing
		}
		parts = append(parts, glyphStyle.Render(glyph)+" "+
			numStyle.Render(fmt.Sprintf("%d", seg.n))+" "+labelStyle.Render(label))
	}
	left := strings.Join(parts, "   ")
	rightText := fmt.Sprintf("%d %s", len(m.rows), lowerDisplay(m.desc.Display))
	switch {
	case m.kind == kube.KindNode:
		rightText = m.nodeSummaryText()
	case m.kind == kube.KindForward:
		rightText = m.forwardSummaryText()
	case m.kind == kube.KindHelmRelease:
		// 18a: "from sh.helm.release.v1 secrets" — names the data source
		// instead of the usual "<N> <kind>" count, since browsing needs no
		// helm binary and the strip already names each status.
		rightText = "from " + string(kube.HelmReleaseSecretType) + " secrets"
	case m.kind == kube.KindCustomResourceDefinition:
		// 14b: "28 definitions · 9 API groups · sorted by group" — the
		// generic "<N> <kind>" wording never names the group count or the
		// sort order this list is unique in guaranteeing.
		rightText = fmt.Sprintf("%d definitions · %d API groups · sorted by group", len(m.rows), distinctCRDGroups(m.rows))
	case m.nodeCount > 0:
		rightText += fmt.Sprintf(" · %d nodes", m.nodeCount)
	}
	if m.grouped() {
		rightText += fmt.Sprintf(" · %d namespaces", distinctNamespaces(m.rows))
	}
	right := labelStyle.Render(rightText)
	return insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)
}

// distinctNamespaces counts the unique namespaces represented in rows, for
// 6b's health-strip "125 pods · 6 namespaces" right side.
func distinctNamespaces(rows []resources.Row) int {
	seen := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		seen[r.Namespace] = struct{}{}
	}
	return len(seen)
}

// distinctCRDGroups counts the unique API groups represented in rows, for
// 14b's health-strip "9 API groups" right side.
func distinctCRDGroups(rows []resources.Row) int {
	seen := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		seen[crdGroupCell(r)] = struct{}{}
	}
	return len(seen)
}

// filterStripLine renders the live "/" query, matched/total, and — when
// rows are hidden — the "esc to clear" notice (docs/design system-wide
// interactions: "items never silently disappear").
func (m Model) filterStripLine(theme tui.Theme, width int) string {
	accent := lipgloss.NewStyle().Foreground(theme.Accent)
	text := lipgloss.NewStyle().Foreground(theme.Text)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)

	left := accent.Render("/ ") + text.Render(m.filterQuery) + accent.Render(tui.GlyphSelBar)

	total, matched := len(m.rows), len(m.visible)
	right := dim.Render(fmt.Sprintf("%d/%d", matched, total))
	if matched < total {
		right = faint.Render(fmt.Sprintf("%d hidden by filter — esc to clear   ", total-matched)) + right
	}
	return insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)
}

// stripInnerWidth/insetStripLine give the health/filter strips the same
// tui.FrameInset horizontal inset as the Frame's chrome bands (mock 2a's
// 14px padding). The empty-state column-header strip skips them — the
// table's own 2-cell marker slot already provides that inset, keeping it
// aligned with the ready-state table.
func stripInnerWidth(width int) int {
	return max(width-2*tui.FrameInset, 0)
}

func insetStripLine(line string, width int) string {
	return components.Pad(strings.Repeat(" ", tui.FrameInset)+line, width)
}

// insetStripLineFill is insetStripLine's background-filled counterpart —
// 4a's offline banner is the one strip line with a real bg (theme.
// ErrBannerBg), so its inset/trailing pad must carry fill's background
// explicitly rather than components.Pad's plain unstyled spaces.
func insetStripLineFill(line string, width int, fill lipgloss.Style) string {
	content := fill.Render(strings.Repeat(" ", tui.FrameInset)) + line
	slack := width - lipgloss.Width(content)
	if slack <= 0 {
		return content
	}
	return content + fill.Render(strings.Repeat(" ", slack))
}

// defaultGlyphFor is the status-column glyph a row falls back to when its
// own projection leaves Row.Glyph unset (every kind but Pods today).
func defaultGlyphFor(class resources.StatusClass) string {
	switch class {
	case resources.StatusOK:
		return tui.GlyphRunning
	case resources.StatusWarn:
		return tui.GlyphPending
	case resources.StatusFail:
		return tui.GlyphFailed
	default:
		return tui.GlyphCompleted
	}
}

func glyphColor(theme tui.Theme, class resources.StatusClass) lipgloss.Color {
	switch class {
	case resources.StatusOK:
		return theme.Good
	case resources.StatusWarn:
		return theme.Warn
	case resources.StatusFail:
		return theme.Bad
	default:
		return theme.Info
	}
}

// padBetween places left-aligned left and right-aligned right within width,
// measuring already-styled (ANSI-containing) strings via lipgloss.Width.
// When there isn't room for both, right is dropped.
func padBetween(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// padBetweenFill is padBetween's background-filled counterpart, for the one
// strip line (4a's offline banner) with a real bg fill — see
// insetStripLineFill's doc comment.
func padBetweenFill(left, right string, width int, fill lipgloss.Style) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + fill.Render(strings.Repeat(" ", gap)) + right
}

func (m Model) Body(width, height int) string {
	if m.pendingSetImage != nil {
		return m.setImageBody(width, height)
	}
	if m.pendingSetResources != nil {
		return m.setResourcesBody(width, height)
	}
	if m.pendingBulkDelete != nil && m.pendingBulkDelete.tier == actions.TierModal {
		return m.bulkDeleteConfirmModal(width, height)
	}
	if m.actions.Active() && m.actions.Tier() == actions.TierModal {
		if pending := m.actions.Pending(); pending != nil && isDeleteVerb(pending.Scope.Verb) {
			return m.deleteConfirmModal(width, height)
		}
		return m.confirmBody(width, height)
	}
	switch m.state {
	case tui.TaskStateEmpty:
		return m.emptyBody(width, height)
	case tui.TaskStateReady:
		return m.tableBody(width, height)
	case tui.TaskStatePermissionDenied:
		return m.permissionDeniedBody(width, height)
	case tui.TaskStateLoading:
		if m.cachedView && len(m.rows) > 0 {
			// 15a: "revisiting a kind seen this session: cached rows dimmed
			// instead of skeletons" — render the real table (muted) over the
			// rowCache snapshot rather than the skeleton loader.
			return m.tableBody(width, height)
		}
		return m.loadingBody(width, height)
	default:
		return components.CenterLines([]string{m.feedback}, width, height)
	}
}

// quotedEntity matches a "..."-quoted span in an RBAC message ("dev-readonly",
// "secrets", "nva-stage", …), for 4b's yellow entity highlighting.
var quotedEntity = regexp.MustCompile(`"[^"]*"`)

// highlightQuoted renders text with base, except any "quoted" spans (RBAC
// messages name the denied user/verb/resource/namespace this way) which
// render through highlight instead — docs/design README.md §4b.
func highlightQuoted(text string, base, highlight lipgloss.Style) string {
	var b strings.Builder
	last := 0
	for _, span := range quotedEntity.FindAllStringIndex(text, -1) {
		b.WriteString(base.Render(text[last:span[0]]))
		b.WriteString(highlight.Render(text[span[0]:span[1]]))
		last = span[1]
	}
	b.WriteString(base.Render(text[last:]))
	return b.String()
}

// permissionDeniedBody is 4b's centered 403 card: title + kind/verb, the
// verbatim RBAC message with quoted entities highlighted, and the recovery
// lines (docs/design README.md §4b) — header stays connected/green (this
// screen only renders when the *load*, not the connection, failed) and
// there's deliberately no auto-retry, only the manual 'r'.
func (m Model) permissionDeniedBody(width, height int) string {
	theme := m.Theme()
	title := lipgloss.NewStyle().Foreground(theme.Bad).Bold(true)
	meta := lipgloss.NewStyle().Foreground(theme.TextFaint)
	body := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	entity := lipgloss.NewStyle().Foreground(theme.Warn)
	key := lipgloss.NewStyle().Foreground(theme.Accent)
	label := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	note := lipgloss.NewStyle().Foreground(theme.TextFaint)

	recover := func(k, l, n string) string {
		line := key.Render(k) + " " + label.Render(l)
		if n != "" {
			line += " " + note.Render("— "+n)
		}
		return line
	}

	lines := []string{
		title.Render("403 Forbidden") + "  " + meta.Render(lowerDisplay(m.desc.Display)+" · list"),
		highlightQuoted(m.feedback, body, entity),
		"",
		recover("g", "jump to another kind", "everything else still works"),
		recover("c", "switch context", "another context may grant access"),
		recover("w", "who-can", "see who does have access"),
		recover("y", "copy error", "paste to your cluster admin"),
		recover("r", "retry", ""),
	}

	cardStyle := lipgloss.NewStyle().
		Foreground(theme.TextSecondary).
		Background(theme.ErrCardBg).
		BorderForeground(theme.ErrCardBorder).
		Padding(1, 2).
		Width(min(60, max(width-8, 20)))
	card := components.Card(strings.Join(lines, "\n"), cardStyle, width, height-2)
	footer := note.Render("last successful list: never · RBAC errors are not retried automatically")
	return card + "\n" + components.Pad(strings.Repeat(" ", max((width-lipgloss.Width(footer))/2, 0))+footer, width)
}

// emptyBody is the 10c centered explainer: "no <kind> in <ns>" plus the
// three live ways out. Each line is built from pre-styled spans (never a
// raw hex literal) per the Theme invariant.
func (m Model) emptyBody(width, height int) string {
	theme := m.Theme()
	text := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	ns := lipgloss.NewStyle().Foreground(theme.AccentHi)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	key := lipgloss.NewStyle().Foreground(theme.Accent)
	detail := lipgloss.NewStyle().Foreground(theme.TextFaint)

	kind := lowerDisplay(m.desc.Display)
	var lines []string
	if m.desc.ClusterScoped {
		// Cluster-scoped kinds (Nodes, Namespaces, Forwards) have no
		// namespace to name — "no X in <ns>" would misdescribe scope the
		// same way the header breadcrumb already drops the namespace
		// segment for them (docs/design README.md §11a/§13c).
		lines = []string{
			text.Render("no " + kind + " yet"),
			dim.Render("the cluster is reachable — there's just nothing here yet"),
			"",
		}
	} else {
		lines = []string{
			text.Render("no "+kind+" in ") + ns.Render(m.namespace),
			dim.Render("the namespace exists and you can read it — there's just nothing here"),
			"",
		}
	}

	if !m.desc.ClusterScoped {
		nsLine := key.Render("n") + " " + dim.Render("switch namespace")
		if m.hints.altNamespace != "" {
			nsLine += " " + detail.Render(fmt.Sprintf("— %s has %d %s", m.hints.altNamespace, m.hints.altCount, kind))
		}
		lines = append(lines, nsLine)

		allLine := key.Render("a") + " " + dim.Render("all namespaces")
		if m.hints.allCount > 0 {
			allLine += " " + detail.Render(fmt.Sprintf("— %d %s cluster-wide", m.hints.allCount, kind))
		}
		lines = append(lines, allLine)

		gotoLine := key.Render("g") + " " + dim.Render("other kinds")
		if len(m.hints.otherKinds) > 0 {
			parts := make([]string, len(m.hints.otherKinds))
			for i, h := range m.hints.otherKinds {
				parts[i] = h.label
			}
			gotoLine += " " + detail.Render("— this namespace has "+strings.Join(parts, ", "))
		}
		lines = append(lines, gotoLine)
	}

	return components.CenterLines(lines, width, height)
}

// rowCellStyles is the docs/design §2a per-column palette, resolved from
// theme once per render: only the status glyph and STATUS text carry the
// status color; the name is TextPrimary (BadText when crashlooping), READY
// TextSecondary, restarts Warn when non-zero, and NODE/AGE/metric values
// TextDim. selBg carries the selection background: for the selected row
// every style (and every ANSI span embedded in a cell — match highlights,
// metric bars) is derived with Background(SelBg), because Table renders the
// selected row per-cell rather than flattening it (mockup 2a: only the name
// brightens).
type rowCellStyles struct {
	name, nameBad, ready, restartsZero, restartsHot, dim, match lipgloss.Style
	bars                                                        components.BarStyles
	status                                                      map[resources.StatusClass]lipgloss.Style
	// customNeutral is 14a's "no status semantics" fallback glyph color
	// (TextFaint) — distinct from status[StatusNeutral]'s Info blue, which
	// every other kind's legitimately-neutral rows (Completed pods,
	// cordoned nodes) keep using.
	customNeutral lipgloss.Style
}

// newRowCellStyles resolves the table's per-cell colors from theme, or —
// when muted — from Theme.Muted plus one step down the text ramp (4a's
// desaturated snapshot: docs/design README.md "filter: saturate(0.35);
// opacity: 0.66"). Each theme declares its own muted ramp rather than this
// computing one, so the two render paths only ever differ in which palette
// they read from. marked bakes in 20a's quieter MarkBg tint instead of SelBg
// for a marked-but-not-selected row — mutually exclusive with selected, since
// "cursor keeps the purple bar" wins when both apply (callers never pass
// both true).
func newRowCellStyles(theme tui.Theme, selected, muted, marked bool) rowCellStyles {
	style := func(fg lipgloss.Color) lipgloss.Style {
		s := lipgloss.NewStyle().Foreground(fg)
		switch {
		case selected:
			s = s.Background(theme.SelBg)
		case marked:
			s = s.Background(theme.MarkBg)
		}
		return s
	}
	good, warnColor, bad, info, primary, secondary, dimColor := theme.Good, theme.Warn, theme.Bad, theme.Info, theme.TextPrimary, theme.TextSecondary, theme.TextDim
	if muted {
		good, warnColor, bad, info = theme.Muted.Good, theme.Muted.Warn, theme.Muted.Bad, theme.Muted.Info
		primary, secondary, dimColor = theme.TextSecondary, theme.TextDim, theme.TextFaint
	}

	name := style(primary)
	if selected {
		name = style(theme.Text) // the one cell that brightens on selection
	}
	nameBad := style(theme.BadText)
	if muted {
		nameBad = style(bad)
	}
	if selected {
		nameBad = name // selection brightening wins over the crashloop tint (mock 2a)
	}
	warn := style(warnColor)
	return rowCellStyles{
		name:         name,
		nameBad:      nameBad,
		ready:        style(secondary),
		restartsZero: style(dimColor),
		restartsHot:  warn,
		dim:          style(dimColor),
		match:        style(theme.AccentHi).Bold(true),
		bars: components.BarStyles{
			Track: style(theme.BarTrack),
			Fill:  style(theme.Accent),
			Warn:  warn,
			// These bars are relative to the busiest visible pod, so the
			// busiest one always sits at ratio 1.0 — that's not "at limit",
			// and 2a has no red bar tier (fill purple, yellow ≥70%). Bad
			// stays reserved for real request/limit bars (5a).
			Bad: warn,
		},
		status: map[resources.StatusClass]lipgloss.Style{
			resources.StatusOK:      style(good),
			resources.StatusWarn:    style(warnColor),
			resources.StatusFail:    style(bad),
			resources.StatusNeutral: style(info),
		},
		customNeutral: style(theme.TextFaint),
	}
}

// tableBody renders the 2a resource table: per-status glyph + STATUS
// coloring, filter match highlighting on Name, live CPU/MEM mini-bars for
// Pods, and a footer range/scrollbar line under the table itself. In 6b's
// grouped mode, m.display (grouping.go's buildDisplayRows) interleaves data
// rows with group header/fold/collapsed-summary lines one-to-one with what
// gets rendered here, so m.selected — which indexes m.display — can be
// handed to Table.Selected directly.
func (m Model) tableBody(width, height int) string {
	theme := m.Theme()
	cols := browseColumns(m.desc)
	cpuMax, memMax := m.metricsMax()
	muted := m.offline() || m.cachedView
	styles := [2]rowCellStyles{newRowCellStyles(theme, false, muted, false), newRowCellStyles(theme, true, muted, false)}
	// marksOn/markedStyle back 20a's mark column and marked-row tint — the
	// mark column only exists while something's marked (13d's zero-chrome
	// rule), so tableCols stays exactly cols when nothing is.
	marksOn := len(m.marks) > 0
	markedStyle := styles[0]
	if marksOn {
		markedStyle = newRowCellStyles(theme, false, muted, true)
	}
	tableCols := cols
	if marksOn {
		tableCols = append([]components.Column{{Title: "", Min: 1}}, cols...)
	}
	majorityVersion := ""
	if m.kind == kube.KindNode {
		majorityVersion = m.nodeMajorityVersion()
	}

	grouped := m.grouped()

	rows := make([]components.Row, 0, len(m.display))
	for idx, dr := range m.display {
		selected := idx == m.selected
		switch dr.kind {
		case rowKindHeader:
			rows = append(rows, components.Row{
				GroupHeader: m.groupHeaderLine(dr.namespace, dr.counts),
				GroupStyle:  groupLineStyle(theme, rowKindHeader, selected),
				GroupRight:  groupHeaderChips(theme, dr.counts, selected),
			})
			continue
		case rowKindCollapsedSummary:
			rows = append(rows, components.Row{
				GroupHeader: m.collapsedSummaryLine(dr.namespace, dr.counts),
				GroupStyle:  groupLineStyle(theme, rowKindCollapsedSummary, selected),
			})
			continue
		case rowKindFold:
			rows = append(rows, components.Row{
				GroupHeader: m.foldLine(dr.folded),
				GroupStyle:  groupLineStyle(theme, rowKindFold, selected),
			})
			continue
		}

		fm := dr.row
		r := fm.row
		isMarkedRow := marksOn && m.isMarked(r)
		st := styles[0]
		switch {
		case selected:
			st = styles[1]
		case isMarkedRow:
			st = markedStyle
		}
		cells := m.rowCells(r, fm.matches, cols, width, st, theme, cpuMax, memMax, majorityVersion, marksOn, isMarkedRow)
		// RowStyle carries st.dim's background (MarkBg for a marked row, none
		// otherwise — Table's own SelRowStyle wins when selected regardless)
		// into the leading marker slot and inter-column gaps, which per-cell
		// Style alone doesn't reach (components.Table's renderRowV2).
		rows = append(rows, components.Row{Cells: cells, RowStyle: st.dim})
	}

	sortKey, sortAsc := "", false
	if workloadKinds[m.kind] && !grouped {
		// The sort indicator implies a straight global status sort; 6b
		// groups by namespace first, so showing "↑" on STATUS would
		// misdescribe the order.
		sortKey, sortAsc = "status", true
	}

	t := components.Table{
		Columns:  tableCols,
		Rows:     rows,
		Selected: m.selected,
		Offset:   m.offset,
		Width:    width,
		// One line reserved for FooterLine, one for the rule dividing the
		// column headers from the data rows (keep in sync with selection.go's
		// tableDataRows).
		Height:         max(height-1, 1),
		SortKey:        sortKey,
		SortAsc:        sortAsc,
		HeaderStyle:    lipgloss.NewStyle().Foreground(theme.TextFaint),
		SortStyle:      lipgloss.NewStyle().Foreground(theme.Accent),
		SelBarStyle:    lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.SelBg),
		SelRowStyle:    lipgloss.NewStyle().Background(theme.SelBg),
		FooterStyle:    lipgloss.NewStyle().Foreground(theme.TextGhost),
		ShowHeaderRule: true,
		RuleStyle:      lipgloss.NewStyle().Foreground(theme.TextGhost2),
	}
	return t.Render() + "\n" + t.FooterLine(width)
}

// rowCells builds one data row's per-column cells with 2a's per-column
// styling — factored out of tableBody's loop above so setImageBody's frozen
// context row (setimage_view.go) renders through the exact same styling as
// the live table, with no pixel drift between the two. matches backs Name's
// filter-highlight spans (nil outside an active filter — setImageBody's
// caller never has one, since pendingSetImage can't open while filterActive).
// marksOn/isMarkedRow false skips the leading mark column entirely
// (setImageBody's one frozen row never renders it, 20a marks being
// orthogonal to a single row's set-image edit).
func (m Model) rowCells(r resources.Row, matches []int, cols []components.Column, width int, st rowCellStyles, theme tui.Theme, cpuMax, memMax int64, majorityVersion string, marksOn, isMarkedRow bool) []components.Cell {
	cells := resources.Cells(r, width, nil)
	for i := range cells {
		switch {
		case i == 0: // status glyph column
			if cells[i].Text == "" {
				cells[i].Text = defaultGlyphFor(r.Status)
			}
			cells[i].Style = st.status[r.Status]
			if m.desc.Custom && r.Status == resources.StatusNeutral {
				// docs/design README.md §14a: a CRD instance with no
				// Ready/Available condition at all — "never fake
				// health" — renders TextFaint, not the generic Neutral/
				// Info blue every other kind's Neutral class (Completed
				// pods, cordoned nodes) uses.
				cells[i].Style = st.customNeutral
			}
		case m.kind == kube.KindPod && cols[i].Title == "CPU":
			cells[i] = m.metricCell(r.Name, true, cpuMax, st)
		case m.kind == kube.KindPod && cols[i].Title == "MEM":
			cells[i] = m.metricCell(r.Name, false, memMax, st)
		case m.kind == kube.KindNode && cols[i].Title == "CPU":
			cells[i] = m.nodeMetricCell(r.Name, true, st)
		case m.kind == kube.KindNode && cols[i].Title == "MEM":
			cells[i] = m.nodeMetricCell(r.Name, false, st)
		case m.kind == kube.KindNode && cols[i].Title == "Pods":
			cells[i] = components.Cell{Text: m.nodePodsCell(r.Name), Style: st.dim}
		case m.kind == kube.KindNode && cols[i].Title == "Version":
			text := cells[i].Text
			if majorityVersion != "" && text != majorityVersion {
				text += " " + tui.GlyphWarning
			}
			cells[i] = components.Cell{Text: text, Style: st.dim}
		case cols[i].Title == "Name":
			base := st.name
			if r.Status == resources.StatusFail {
				base = st.nameBad
			}
			text := highlightName(r.Name, matches, st.match, base)
			if r.NameSuffix != "" {
				text += st.dim.Render(r.NameSuffix)
			}
			cells[i] = components.Cell{Text: text}
		case cols[i].Title == "Ready":
			cells[i].Style = st.ready
		case m.kind == kube.KindNode && cols[i].Title == "Status":
			// docs/design README.md §11a: "healthy state renders dim,
			// not green" — the same carve-out 9a's ROLLOUT column
			// already gets (rowKindHeader case above), extended to
			// Nodes' own STATUS column: Ready dims, NotReady/other keep
			// the usual status color.
			cells[i].Style = st.dim
			if r.Status != resources.StatusOK {
				cells[i].Style = st.status[r.Status]
			}
		case cols[i].Title == "Status":
			cells[i].Style = st.status[r.Status]
		case cols[i].Title == "Rollout":
			// docs/design README.md §9a: "stable dim" — unlike STATUS,
			// the healthy rollout state renders dim rather than green;
			// progressing/degraded keep their usual warn/bad coloring.
			cells[i].Style = st.dim
			if r.Status != resources.StatusOK {
				cells[i].Style = st.status[r.Status]
			}
		case cols[i].Title == tui.GlyphRestarts:
			cells[i].Style = st.restartsHot
			if cells[i].Text == "0" {
				cells[i].Style = st.restartsZero
			}
		default:
			cells[i].Style = st.dim
		}
	}
	if marksOn {
		// 20a's leading mark cell: ▪ (purple) when marked, blank
		// otherwise — st.dim already carries the right background
		// (SelBg/MarkBg/none) for this row, so overriding just the
		// foreground keeps the row's background one continuous fill.
		markCell := components.Cell{Style: st.dim}
		if isMarkedRow {
			markCell = components.Cell{Text: tui.GlyphMarked, Style: st.dim.Foreground(theme.Accent)}
		}
		cells = append([]components.Cell{markCell}, cells...)
	}
	return cells
}

// groupHeaderLine draws one 6b namespace group's ▾ heading (docs/design
// README.md §6b): name, row count, and a trouble summary — "all running"
// when every row in the group is healthy (only reachable once the group is
// manually expanded — a collapsed, fully-healthy group renders through
// collapsedSummaryLine instead), else the non-zero classes named via the
// kind's own HealthLabel wording (worst first). Table.GroupHeader renders
// through a single per-row style (no per-segment coloring like the 2a
// health strip's per-glyph colors), so this stays plain text; Table owns
// the leading "  "/"▎ " marker column, so this — like collapsedSummaryLine/
// foldLine — doesn't add its own.
func (m Model) groupHeaderLine(namespace string, counts resources.HealthCounts) string {
	name := namespace
	if name == "" {
		name = "(none)"
	}
	line := fmt.Sprintf("%s %s · %d %s", tui.GlyphCollapse, name, counts.Total(), lowerDisplay(m.desc.Display))
	if counts.Fail+counts.Warn == 0 {
		// Nothing to flag on the right — an expanded, fully-healthy group
		// (collapsedSummaryLine handles the default collapsed case) still
		// says so inline, same as before this had right-aligned chips.
		line += " · all running"
	}
	return line
}

// groupHeaderChips builds 6b's right-aligned trouble chips (docs/design
// README.md §6b: "right-aligned trouble chips (◐2 ✕1)") — one per non-zero
// class, each its own status color, replacing the inline "N crashloop · M
// pending" text groupHeaderLine used to append. Empty when the group has
// nothing to flag.
func groupHeaderChips(theme tui.Theme, counts resources.HealthCounts, selected bool) []components.Cell {
	bg := theme.BgChrome
	if selected {
		bg = theme.SelBg
	}
	var cells []components.Cell
	add := func(class resources.StatusClass, n int) {
		if n == 0 {
			return
		}
		style := lipgloss.NewStyle().Foreground(glyphColor(theme, class)).Background(bg)
		cells = append(cells, components.Cell{Text: fmt.Sprintf("%s%d", defaultGlyphFor(class), n), Style: style})
	}
	add(resources.StatusWarn, counts.Warn)
	add(resources.StatusFail, counts.Fail)
	return cells
}

// collapsedSummaryLine draws 6b's fully-healthy group's single line,
// replacing the ▾ header entirely: the ▸ glyph, namespace, total count, and
// "all running" — green (groupLineStyle), the app's only other collapse/
// expand affordance besides yamlview's managedFields fold.
func (m Model) collapsedSummaryLine(namespace string, counts resources.HealthCounts) string {
	name := namespace
	if name == "" {
		name = "(none)"
	}
	return fmt.Sprintf("%s %s · %d %s · all running", tui.GlyphExpand, name, counts.Total(), lowerDisplay(m.desc.Display))
}

// foldLine draws 6b's "+N running · ↹ expand" tail standing in for a
// partially-shown group's folded healthy rows.
func (m Model) foldLine(folded int) string {
	return fmt.Sprintf("+ %d %s · %s expand", folded, m.desc.HealthLabel(resources.StatusOK), tui.GlyphTab)
}

// groupLineStyle resolves 6b's three group-line colors from theme — purple
// for the normal ▾ header (AccentHi, unchanged from pre-collapse), gray for
// both the fully-healthy ▸ summary and the "+N running" fold tail
// (TextFaint) — a fully-healthy namespace has nothing to triage, so it's
// deliberately de-emphasized rather than drawing the eye with green — baking
// in theme.SelBg when selected, the same selected-variant convention
// newRowCellStyles already uses for cells, so a selected group line's
// background reads as one continuous fill across Table's bar + content
// split (renderGroupRowV2).
func groupLineStyle(theme tui.Theme, kind displayRowKind, selected bool) lipgloss.Style {
	fg := theme.AccentHi
	switch kind {
	case rowKindCollapsedSummary, rowKindFold:
		fg = theme.TextFaint
	}
	s := lipgloss.NewStyle().Foreground(fg)
	switch {
	case selected:
		s = s.Background(theme.SelBg)
	case kind == rowKindHeader:
		// docs/design README.md §6b: "group header line (bg #0e0e15)" — a
		// distinguishing fill the collapsed-summary/fold lines don't get.
		s = s.Background(theme.BgChrome)
	}
	return s
}

// metricCell renders one Pod row's CPU/MEM cell: a MiniBar scaled to the
// busiest currently-visible pod, then the metric's compact value in dim
// (mockup 2a's bar-then-number order; no request/limit data reaches
// browse's Row, so this is a relative-usage bar rather than a request/limit
// zone bar) — "–" for both while metrics haven't loaded yet.
func (m Model) metricCell(name string, cpu bool, maxVal int64, st rowCellStyles) components.Cell {
	const barWidth = 6
	valWidth := resources.MetricColumnWidth - barWidth - 1

	pm, ok := m.podMetrics[name]
	value, used := "–", int64(0)
	if ok {
		if cpu {
			value, used = pm.CPU, pm.CPUMilli
		} else {
			value, used = pm.MEM, pm.MemBytes
		}
		if value == "" || value == "n/a" {
			value = "–"
		}
	}
	// The separating space renders through st.dim too, so a selected row's
	// background stays continuous across the cell.
	valText := st.dim.Render(" " + components.Truncate(value, valWidth))
	return components.Cell{Text: components.MiniBar(used, maxVal, barWidth, st.bars) + valText}
}

// metricsMax finds the busiest CPU/MEM usage across the currently loaded
// rows, the bar denominator described in metricCell.
func (m Model) metricsMax() (cpuMax, memMax int64) {
	for _, r := range m.rows {
		if pm, ok := m.podMetrics[r.Name]; ok {
			cpuMax = max(cpuMax, pm.CPUMilli)
			memMax = max(memMax, pm.MemBytes)
		}
	}
	return cpuMax, memMax
}
