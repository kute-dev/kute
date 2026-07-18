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
