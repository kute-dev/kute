package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// sgrPrefix is the escape sequence a style emits before its text.
func sgrPrefix(style lipgloss.Style) string {
	out := style.Render("x")
	return out[:strings.Index(out, "x")]
}

// TestKeybarFailedResult pins review 10 H1's shared render path: a failed
// ResultLine replaces the hint groups, renders in the theme's Bad token
// behind GlyphFailed in both themes, folds a multi-line error onto one
// line, and truncates rather than dropping when it is too long to fit.
func TestKeybarFailedResult(t *testing.T) {
	for name, theme := range map[string]Theme{"dark": Dark(), "light": Light()} {
		t.Run(name, func(t *testing.T) {
			kb := Keybar{
				Pill: ModeBrowse, PillText: "PODS",
				Groups:     [][]KeyHint{{{Key: "l", Label: "logs"}}},
				RightHints: []KeyHint{{Key: "?", Label: "help"}},
				Result:     ResultLine{Text: "Delete Pod api-0 failed: forbidden\n" + strings.Repeat("x", 200), Failed: true},
			}
			out := renderKeybarV2(kb, theme, 100)
			text := ansi.Strip(out)
			if !strings.Contains(text, GlyphFailed+" Delete Pod api-0 failed: forbidden x") {
				t.Fatalf("keybar = %q, want the folded error", text)
			}
			if strings.Contains(text, "logs") {
				t.Fatalf("keybar = %q, the error replaces the hint groups", text)
			}
			if ansi.StringWidth(text) != 100 || !strings.Contains(text, "...") {
				t.Fatalf("keybar = %q, want a truncated full-width line", text)
			}
			if !strings.Contains(text, "? help") {
				t.Fatalf("keybar = %q, want ? help kept", text)
			}
			if !strings.Contains(out, sgrPrefix(lipgloss.NewStyle().Foreground(theme.Bad))) {
				t.Fatalf("failed result not rendered in theme.Bad: %q", out)
			}
		})
	}
}

// TestKeybarSuccessResult: a success keeps the hints and answers green on
// the right, ahead of any will-run note.
func TestKeybarSuccessResult(t *testing.T) {
	for name, theme := range map[string]Theme{"dark": Dark(), "light": Light()} {
		t.Run(name, func(t *testing.T) {
			kb := Keybar{
				Pill: ModeBrowse, PillText: "PODS",
				Groups: [][]KeyHint{{{Key: "l", Label: "logs"}}},
				Result: ResultLine{Text: "Delete Pod api-0"},
			}
			out := renderKeybarV2(kb, theme, 100)
			text := ansi.Strip(out)
			if !strings.Contains(text, "logs") || !strings.Contains(text, GlyphSucceeded+" Delete Pod api-0") {
				t.Fatalf("keybar = %q, want hints plus the success line", text)
			}
			if !strings.Contains(out, sgrPrefix(lipgloss.NewStyle().Foreground(theme.Good))) {
				t.Fatalf("success not rendered in theme.Good: %q", out)
			}
		})
	}
}
