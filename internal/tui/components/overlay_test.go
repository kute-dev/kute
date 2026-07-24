package components

import (
	"charm.land/lipgloss/v2"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestComposeSizeIsFixed(t *testing.T) {
	t.Parallel()
	base := "aaaaaaaaaa\nbbbbbbbbbb\ncccccccccc\ndddddddddd"
	panel := "XX\nXX"
	got := Compose(base, panel, 10, 4, 1, lipgloss.NewStyle())
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), got)
	}
	for i, l := range lines {
		if w := runewidth.StringWidth(l); w != 10 {
			t.Fatalf("line %d width = %d, want 10: %q", i, w, l)
		}
	}
}

func TestComposeSplicesPanelAnchoredAndCentered(t *testing.T) {
	t.Parallel()
	base := strings.Repeat("a", 20) + "\n" + strings.Repeat("a", 20) + "\n" + strings.Repeat("a", 20) + "\n" + strings.Repeat("a", 20)
	panel := "XXXX\nXXXX"
	got := Compose(base, panel, 20, 4, 1, lipgloss.NewStyle())
	lines := strings.Split(got, "\n")
	// anchored at top = 1; width 20, panel width 4 -> horizontally centered at 8
	if !strings.Contains(lines[1], "XXXX") {
		t.Fatalf("expected panel row 1 to contain XXXX: %q", lines[1])
	}
	if !strings.Contains(lines[2], "XXXX") {
		t.Fatalf("expected panel row 2 to contain XXXX: %q", lines[2])
	}
	if strings.Contains(lines[0], "XXXX") || strings.Contains(lines[3], "XXXX") {
		t.Fatalf("panel bled outside its rows:\n%s", got)
	}
	if idx := strings.Index(lines[1], "XXXX"); idx != 8 {
		t.Fatalf("panel left offset = %d, want 8: %q", idx, lines[1])
	}
}

func TestComposeClampsTopSoPanelStaysOnScreen(t *testing.T) {
	t.Parallel()
	base := strings.Repeat("aaaa\n", 3) + "aaaa"
	got := Compose(base, "XX\nXX", 10, 4, 99, lipgloss.NewStyle())
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[2], "XX") || !strings.Contains(lines[3], "XX") {
		t.Fatalf("expected an oversized top clamped so the panel still shows at the bottom:\n%s", got)
	}
}

func TestComposeStripsBaseColorBeforeDimming(t *testing.T) {
	t.Parallel()
	// A base line already containing ANSI color codes should come out with
	// none, since Compose re-renders every line plain through dimStyle.
	colored := "\x1b[31mred\x1b[0m"
	got := Compose(colored, "", 10, 1, 0, lipgloss.NewStyle())
	if strings.Contains(got, "\x1b[31m") {
		t.Fatalf("expected base color codes stripped before dimming: %q", got)
	}
}

func TestComposePadsShortBase(t *testing.T) {
	t.Parallel()
	got := Compose("only one line", "", 20, 5, 0, lipgloss.NewStyle())
	lines := strings.Split(got, "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5", len(lines))
	}
}
