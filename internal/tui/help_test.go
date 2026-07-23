package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// fakeHelpScreen is a minimal Screen stub — renderHelp only ever calls
// Keybar() on the active screen, so every other method is a stub.
type fakeHelpScreen struct {
	kb Keybar
}

func (f fakeHelpScreen) Theme() Theme         { return Dark() }
func (f fakeHelpScreen) Header() HeaderState  { return HeaderState{} }
func (f fakeHelpScreen) Strips(int) []string  { return nil }
func (f fakeHelpScreen) Keybar() Keybar       { return f.kb }
func (f fakeHelpScreen) Body(_, _ int) string { return "" }

// TestRenderHelpDoesNotTruncateColumns pins the 7b fix (docs/design
// README.md §113-114): columns are sized against the row's real budget
// (helpInset's frameWidth-2), not the full frame width — the previous
// off-by-two truncated every row with a stray "…" at most terminal widths.
func TestRenderHelpDoesNotTruncateColumns(t *testing.T) {
	view := fakeHelpScreen{kb: Keybar{
		PillText: "POD",
		Groups: [][]KeyHint{{
			{Key: "l", Label: "logs"},
			{Key: "y", Label: "yaml"},
		}},
	}}
	scope := []KeyHint{{Key: "g", Label: "goto"}, {Key: "n", Label: "namespace"}}
	global := []KeyHint{{Key: "?", Label: "help"}, {Key: "ctrl+q", Label: "quit"}}

	for _, width := range []int{80, 100, 120, 160, 220} {
		panel := renderHelp(Dark(), view, scope, global, width)
		plain := ansi.Strip(panel)
		if strings.Contains(plain, "…") {
			t.Errorf("width %d: rendered help panel truncated a column with '…':\n%s", width, plain)
		}
	}
}
