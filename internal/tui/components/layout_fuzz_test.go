package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// FuzzTruncateAndPad fuzzes the two functions every cell in every table goes
// through. Their input is arbitrary cluster data — pod names, log lines,
// annotation values — which can carry ANSI escapes, wide CJK runes, combining
// marks, zero-width joiners and invalid UTF-8.
//
// The properties are the ones the whole layout depends on:
//   - Truncate never returns something wider than the width it was given;
//   - Pad returns *exactly* that width, which is what keeps columns aligned —
//     one over-wide cell shifts every column to its right on that row;
//   - both hold per line, since a multi-line value is handled line-by-line.
//
// A width bigger than the string is the common case and must be a no-op, so
// the width is derived from the input rather than fixed.
func FuzzTruncateAndPad(f *testing.F) {
	seeds := []struct {
		s string
		w int
	}{
		{"", 0}, {"", 10}, {"plain", 3}, {"plain", 5}, {"plain", 100},
		{"exactly", 1}, {"exactly", 2}, {"exactly", 3}, {"exactly", 4},
		{"\x1b[31mred\x1b[0m", 2},
		{"日本語のテキスト", 5},
		{"écombining", 4},
		{"👩‍👩‍👧‍👦 family zwj", 6},
		{"two\nlines here", 3},
		{"\xff\xfe invalid", 4},
		{strings.Repeat("wide", 500), 17},
	}
	for _, s := range seeds {
		f.Add(s.s, s.w)
	}

	f.Fuzz(func(t *testing.T, value string, width int) {
		// Only the meaningful range: Truncate documents width <= 0 as empty,
		// and an enormous width just makes both a no-op.
		if width < 0 || width > 1000 {
			t.Skip()
		}

		for _, line := range strings.Split(Truncate(value, width), "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("Truncate(%q, %d) produced a %d-cell line %q", value, width, got, line)
			}
		}

		for _, line := range strings.Split(Pad(value, width), "\n") {
			if got := ansi.StringWidth(line); got != width {
				t.Fatalf("Pad(%q, %d) produced a %d-cell line %q — columns to its right will shift", value, width, got, line)
			}
		}
	})
}
