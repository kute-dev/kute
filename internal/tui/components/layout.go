package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Truncate ellipsizes value to width cells. A value already word-wrapped
// into multiple lines (embedded "\n", e.g. setup's raw-error box after
// lipgloss.Style.Width triggers its own wrap) is truncated line-by-line —
// ansi.StringWidth has no concept of line breaks and would otherwise measure
// the wrapped lines' combined length as one run, truncating the whole block
// down to a bare "…" and silently dropping every line after the first
// (docs/design README.md §4c).
func Truncate(value string, width int) string {
	if strings.Contains(value, "\n") {
		lines := strings.Split(value, "\n")
		for i, l := range lines {
			lines[i] = Truncate(l, width)
		}
		return strings.Join(lines, "\n")
	}
	if width <= 0 {
		return ""
	}
	// Invalid UTF-8 does not measure additively: a stray lead byte swallows
	// whatever follows it, so ansi.StringWidth(a+b) != StringWidth(a) +
	// StringWidth(b), and every width calculation downstream is wrong by
	// however many bytes got absorbed. Cluster data really can carry
	// arbitrary bytes — an annotation value, a log line — so normalise here,
	// at the one boundary every rendered cell passes through. Valid input is
	// untouched.
	value = strings.ToValidUTF8(value, "\uFFFD")
	if ansi.StringWidth(value) <= width {
		return value
	}
	ellipsis := "…"
	if width <= 3 {
		ellipsis = ""
	}
	out := ansi.Truncate(value, width, ellipsis)
	if ansi.StringWidth(out) <= width {
		return out
	}
	// The cut landed inside a malformed escape sequence — a bare ESC in a log
	// line, say — leaving a fragment that measures wider than the budget.
	// Dropping the styling is the cheaper loss: an over-wide cell pushes every
	// column to its right off the row.
	return ansi.Truncate(ansi.Strip(value), width, ellipsis)
}

// Pad truncates/pads value to exactly width cells, per line — see
// Truncate's doc comment on multi-line handling.
func Pad(value string, width int) string {
	if strings.Contains(value, "\n") {
		lines := strings.Split(value, "\n")
		for i, l := range lines {
			lines[i] = Pad(l, width)
		}
		return strings.Join(lines, "\n")
	}
	// Truncate has already replaced any invalid UTF-8, so the width it
	// reports is the width the padding below can rely on.
	value = Truncate(value, width)
	if out, ok := padToWidth(value, width); ok {
		return out
	}
	// The value ends mid-escape-sequence — a bare ESC in a log line, or a
	// style Truncate cut through — so the terminal reads the padding as part
	// of that sequence however much of it there is. Styling is the cheaper
	// thing to lose: a cell that renders narrow shifts every column to its
	// right for the whole row.
	plain := ansi.Strip(value)
	if out, ok := padToWidth(plain, width); ok {
		return out
	}
	return Truncate(plain, width)
}

// padToWidth appends spaces until value measures exactly width cells,
// reporting whether it got there.
//
// It has to measure rather than calculate, because cell width is not additive
// across a concatenation boundary: a prepending combining mark (U+0600 and its
// family) fuses with the first pad space and the pair renders as one cell, so
// width-StringWidth(value) spaces comes up short. The loop is bounded — a
// value carrying a dangling escape sequence swallows every space it is given
// and can never reach width, and that case has its own answer at the call
// site.
func padToWidth(value string, width int) (string, bool) {
	out := value
	for range width + 1 {
		switch w := ansi.StringWidth(out); {
		case w == width:
			return out, true
		case w > width:
			return out, false
		default:
			out += strings.Repeat(" ", width-w)
		}
	}
	return out, ansi.StringWidth(out) == width
}

// Wrap word-wraps plain (unstyled) s to width-wide lines via lipgloss's own
// Width-based reflow — s must stay ANSI-free going in for the wrap width
// math to be accurate. Callers style each returned line individually.
func Wrap(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	return strings.Split(lipgloss.NewStyle().Width(width).Render(s), "\n")
}

func NonColorMarker(active bool) string {
	if active {
		return "▸"
	}
	return " "
}

// CenterLines horizontally centers each line within width and vertically
// centers the whole block by leading with blank lines, leaving the caller
// (Frame's fitBody) to pad the remainder at the bottom — used by empty/error/
// loading bodies (10c, 4b, …) that show a short explainer over an otherwise
// full-height area.
func CenterLines(lines []string, width, height int) string {
	style := lipgloss.NewStyle().Width(width).Align(lipgloss.Center)
	top := (height - len(lines)) / 2
	if top < 0 {
		top = 0
	}
	out := make([]string, 0, top+len(lines))
	blank := style.Render("")
	for range top {
		out = append(out, blank)
	}
	for _, l := range lines {
		out = append(out, style.Render(l))
	}
	return strings.Join(out, "\n")
}
