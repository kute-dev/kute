package components

import (
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestTruncate(t *testing.T) {
	if got := Truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("Truncate() = %q", got)
	}
}

func TestPadUsesTerminalCellWidth(t *testing.T) {
	got := Pad("abc…", 6)
	if got != "abc…  " {
		t.Fatalf("Pad() = %q", got)
	}
}

// TestPadHandlesEmbeddedNewlinesPerLine pins 4c's word-wrap truncation bug
// (docs/design README.md §4c): a value already wrapped into multiple lines
// (e.g. by lipgloss.Style.Width) must be padded/truncated line-by-line, not
// measured as one run — the old ansi.StringWidth-over-the-whole-string
// behavior truncated a realistic two-line error down to a bare "…",
// dropping the second line's content entirely.
func TestPadHandlesEmbeddedNewlinesPerLine(t *testing.T) {
	value := "dial tcp 10.0.0.5:16443:\ni/o timeout after 30s"
	got := Pad(value, 30)
	want := "dial tcp 10.0.0.5:16443:      \ni/o timeout after 30s         "
	if got != want {
		t.Fatalf("Pad(multiline) = %q, want %q", got, want)
	}
}

func TestTruncateDoesNotSplitUnicode(t *testing.T) {
	got := Truncate("alertmanager-prometheus-0", 14)
	if got != "alertmanager-…" {
		t.Fatalf("Truncate() = %q", got)
	}
	if runewidth.StringWidth(got) != 14 {
		t.Fatalf("Truncate() width = %d", runewidth.StringWidth(got))
	}
}

func TestClampRange(t *testing.T) {
	r := ClampRange(8, 5, 10)
	if r.Start != 8 || r.End != 10 || r.Total != 10 {
		t.Fatalf("ClampRange() = %+v", r)
	}
}

func TestNonColorMarker(t *testing.T) {
	if NonColorMarker(true) == NonColorMarker(false) {
		t.Fatal("active and inactive markers must differ")
	}
}
