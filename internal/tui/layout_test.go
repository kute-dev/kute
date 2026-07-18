package tui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestNormalizeSize(t *testing.T) {
	t.Parallel()

	got := NormalizeSize(0, -1)
	if got.Width != DefaultWidth || got.Height != DefaultHeight {
		t.Fatalf("NormalizeSize() = %+v, want default %dx%d", got, DefaultWidth, DefaultHeight)
	}
}

func TestBodyHeight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		height int
		want   int
	}{
		{name: "normal", height: 24, want: 22},
		{name: "tiny", height: 1, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := BodyHeight(tt.height); got != tt.want {
				t.Fatalf("BodyHeight(%d) = %d, want %d", tt.height, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	got := Truncate("abcdefgh", 5)
	want := "ab..."
	if got != want {
		t.Fatalf("Truncate() = %q, want %q", got, want)
	}
}

func TestTruncateAndPadLineAreDisplayWidthSafe(t *testing.T) {
	t.Parallel()

	got := Truncate("CPU ███░░", 8)
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("Truncate produced replacement rune: %q", got)
	}
	if width := runewidth.StringWidth(got); width > 8 {
		t.Fatalf("Truncate width = %d, want <= 8 for %q", width, got)
	}

	padded := PadLine("● Running", 12)
	if strings.ContainsRune(padded, '\uFFFD') {
		t.Fatalf("PadLine produced replacement rune: %q", padded)
	}
	if width := runewidth.StringWidth(padded); width != 12 {
		t.Fatalf("PadLine width = %d, want 12 for %q", width, padded)
	}
}
