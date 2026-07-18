package yamlview

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/tui"
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
	ghost := lipgloss.NewStyle().Foreground(theme.TextGhost)
	text := lipgloss.NewStyle().Foreground(theme.Text).Bold(true)

	ctxName := "cluster unavailable"
	if m.session != nil && m.session.Location.Context != "" {
		ctxName = m.session.Location.Context
	}

	crumbs := []tui.Crumb{
		{Text: "kute", Style: accent},
		{Text: " │ ", Style: ghost},
		{Text: ctxName, Style: dim},
		{Text: " › ", Style: ghost},
		{Text: m.name, Style: dim},
		{Text: " › ", Style: ghost},
		{Text: "YAML", Style: text},
	}

	return tui.HeaderState{
		Crumbs:      crumbs,
		ForwardChip: tui.BuildForwardChip(theme, m.session.ForwardSummary()),
		Conn:        tui.LiveConnBadge(theme, m.conn, tui.GlyphRunning+" live · updates as object changes"),
	}
}

// Strips renders 8a's info strip (kind/name/resourceVersion + fold summary)
// and, while searching, a second strip mirroring browse's filterStripLine
// shape.
func (m Model) Strips(width int) []string {
	if m.state != tui.TaskStateReady {
		return nil
	}
	theme := m.Theme()
	var lines []string
	if m.isSecret {
		lines = append(lines, m.secretStripLine(theme, width))
	} else {
		lines = append(lines, m.infoStripLine(theme, width))
	}
	if m.searchActive {
		lines = append(lines, m.searchStripLine(theme, width))
	}
	return lines
}

func (m Model) infoStripLine(theme tui.Theme, width int) string {
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)

	left := dim.Render(string(m.kind) + " " + m.name)
	right := dim.Render("resourceVersion " + m.resourceVersion)
	if folded := foldedCount(m.folded); folded > 0 {
		right = faint.Render(fmt.Sprintf("%d folded   ", folded)) + right
	}
	return padBetween(left, right, width)
}

// secretStripLine is 21a's own info strip: kind/type/key-count/revealed-count
// in place of the generic kind+name/resourceVersion pair, plus the
// never-persisted safety note in place of the fold count.
func (m Model) secretStripLine(theme tui.Theme, width int) string {
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)

	revealedCount := 0
	for _, e := range m.secretData {
		if m.revealed[e.key] {
			revealedCount++
		}
	}
	left := dim.Render(fmt.Sprintf("Secret · %s · %d keys · %d revealed", m.secretType, len(m.secretData), revealedCount))
	right := faint.Render("decoded in memory only — never logged, never on disk")
	return padBetween(left, right, width)
}

func (m Model) searchStripLine(theme tui.Theme, width int) string {
	accent := lipgloss.NewStyle().Foreground(theme.Accent)
	text := lipgloss.NewStyle().Foreground(theme.Text)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)

	left := accent.Render("/ ") + text.Render(m.searchQuery) + accent.Render(tui.GlyphSelBar)
	right := dim.Render(strconv.Itoa(searchMatchCount(m.rendered(), m.searchQuery)) + " matches")
	return padBetween(left, right, width)
}

func padBetween(left, right string, width int) string {
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

func foldedCount(folded map[string]bool) int {
	n := 0
	for _, v := range folded {
		if v {
			n++
		}
	}
	return n
}

func searchMatchCount(rendered []renderLine, query string) int {
	if query == "" {
		return 0
	}
	q := strings.ToLower(query)
	n := 0
	for _, rl := range rendered {
		if strings.Contains(strings.ToLower(rl.Text), q) {
			n++
		}
	}
	return n
}

func (m Model) Body(width, height int) string {
	switch m.state {
	case tui.TaskStateReady:
		return m.readyBody(width, height)
	case tui.TaskStateLoading:
		style := lipgloss.NewStyle().Foreground(m.Theme().Accent)
		return components.LoadingBody(m.spinner, style, m.feedback, width, height)
	default:
		return components.CenterLines([]string{m.feedback}, width, height)
	}
}

func (m Model) readyBody(width, height int) string {
	theme := m.Theme()
	rendered := m.rendered()
	rows := min(height, max(len(rendered)-m.offset, 0))

	gutterWidth := len(strconv.Itoa(len(m.lines))) + 1
	query := strings.ToLower(m.searchQuery)

	lines := make([]string, 0, rows)
	for i := m.offset; i < m.offset+rows && i < len(rendered); i++ {
		lines = append(lines, m.renderRow(theme, rendered[i], i == m.cursor, gutterWidth, width, query))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderRow(theme tui.Theme, rl renderLine, selected bool, gutterWidth, width int, searchQuery string) string {
	gutterStyle := lipgloss.NewStyle().Foreground(theme.TextGhost)
	gutterText := ""
	if rl.LineNo > 0 {
		gutterText = strconv.Itoa(rl.LineNo)
	}
	var bg lipgloss.TerminalColor = lipgloss.NoColor{}
	if selected {
		bg = theme.SelBg
	} else if searchQuery != "" && strings.Contains(strings.ToLower(rl.Text), searchQuery) {
		bg = theme.SelBg
	}
	gutter := gutterStyle.Background(bg).Render(fmt.Sprintf("%*s ", gutterWidth, gutterText))

	var content string
	switch {
	case rl.SecretRevealed:
		content = renderSecretRevealedLine(theme, rl, bg)
	case rl.SecretKey != "":
		content = renderSecretMaskedLine(theme, rl, bg)
	case rl.FoldKey != "":
		content = lipgloss.NewStyle().Foreground(theme.YamlFold).Background(bg).Render(rl.Text)
	default:
		content = renderTokens(theme, TokenizeLine(rl.Text), bg)
	}
	return gutter + components.Pad(content, width-gutterWidth-1)
}

// secretKeyPrefixSplit splits a secret renderLine's text into its leading
// indent and, when the line is the entry's own "key: " line (as opposed to
// a multi-line decoded value's plain-content continuation line), the value
// past it — the key-aware alternative to guessing from ": " which would
// misfire on decoded content that itself contains ": ".
func secretKeyPrefixSplit(rl renderLine) (indent, value string, isKeyLine bool) {
	trimmed := strings.TrimLeft(rl.Text, " ")
	indent = rl.Text[:len(rl.Text)-len(trimmed)]
	prefix := rl.SecretKey + ": "
	if !strings.HasPrefix(trimmed, prefix) {
		return indent, rl.Text, false
	}
	return indent, trimmed[len(prefix):], true
}

// renderSecretMaskedLine colors a masked data: entry's key normally
// (YamlKey) but its placeholder value in a muted tone distinct from real
// string data (theme.YamlStr) — visually, "this is not the real value".
func renderSecretMaskedLine(theme tui.Theme, rl renderLine, bg lipgloss.TerminalColor) string {
	indent, value, isKeyLine := secretKeyPrefixSplit(rl)
	valStyle := lipgloss.NewStyle().Foreground(theme.TextGhost).Background(bg)
	if !isKeyLine {
		return valStyle.Render(rl.Text)
	}
	keyStyle := lipgloss.NewStyle().Foreground(theme.YamlKey).Background(bg)
	return indent + keyStyle.Render(rl.SecretKey+":") + valStyle.Render(" "+value)
}

// renderSecretRevealedLine colors a revealed value in the real string color
// (making it visually indistinguishable from ordinary YAML strings except
// for the trailing "revealed" tag) — continuation lines of a multi-line
// decoded value carry no "key:" prefix and get no tag of their own.
func renderSecretRevealedLine(theme tui.Theme, rl renderLine, bg lipgloss.TerminalColor) string {
	indent, value, isKeyLine := secretKeyPrefixSplit(rl)
	valStyle := lipgloss.NewStyle().Foreground(theme.YamlStr).Background(bg)
	if !isKeyLine {
		return valStyle.Render(rl.Text)
	}
	keyStyle := lipgloss.NewStyle().Foreground(theme.YamlKey).Background(bg)
	return indent + keyStyle.Render(rl.SecretKey+":") + valStyle.Render(" "+value) + " " + revealedTag(theme)
}

// revealedTag is 21a's "bordered revealed tag" — a filled pill (the
// terminal-idiom equivalent of the mockup's bordered chip) using Theme.Warn,
// the same cautionary hue the app reserves for "pay attention here".
func revealedTag(theme tui.Theme) string {
	return lipgloss.NewStyle().
		Foreground(theme.Bg).
		Background(theme.Warn).
		Bold(true).
		Padding(0, 1).
		Render("revealed")
}

func renderTokens(theme tui.Theme, tokens []Token, bg lipgloss.TerminalColor) string {
	var b strings.Builder
	for _, t := range tokens {
		style := lipgloss.NewStyle().Background(bg)
		switch t.Class {
		case Key:
			style = style.Foreground(theme.YamlKey)
		case String:
			style = style.Foreground(theme.YamlStr)
		case Number:
			style = style.Foreground(theme.Warn)
		case Punct:
			style = style.Foreground(theme.YamlPunct)
		case Comment:
			style = style.Foreground(theme.TextDim)
		default:
			style = style.Foreground(theme.TextSecondary)
		}
		b.WriteString(style.Render(t.Text))
	}
	return b.String()
}
