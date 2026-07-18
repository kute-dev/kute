package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func TestMiniBarUnavailableMetrics(t *testing.T) {
	t.Parallel()
	got := MiniBar(0, 0, 6, BarStyles{})
	if !strings.Contains(got, "–") {
		t.Fatalf("expected placeholder dash, got %q", got)
	}
	if runewidth.StringWidth(got) != 6 {
		t.Fatalf("width = %d, want 6: %q", runewidth.StringWidth(got), got)
	}
}

func TestMiniBarFillsProportionally(t *testing.T) {
	t.Parallel()
	got := MiniBar(5, 10, 10, BarStyles{})
	if n := strings.Count(got, "■"); n != 5 {
		t.Fatalf("filled cells = %d, want 5: %q", n, got)
	}
	if n := strings.Count(got, "□"); n != 5 {
		t.Fatalf("track cells = %d, want 5: %q", n, got)
	}
}

// TestFillStyleForThresholds exercises the threshold logic directly: golden
// comparisons run colorless (no TTY ⇒ lipgloss emits no ANSI), so rendered
// MiniBar output can't distinguish styles by string content. Compare the
// selected style's foreground color instead.
func TestFillStyleForThresholds(t *testing.T) {
	t.Parallel()
	styles := BarStyles{
		Fill: lipgloss.NewStyle().Foreground(lipgloss.Color("#a78bfa")),
		Warn: lipgloss.NewStyle().Foreground(lipgloss.Color("#e8c74a")),
		Bad:  lipgloss.NewStyle().Foreground(lipgloss.Color("#ef6a6a")),
	}
	tests := []struct {
		name  string
		ratio float64
		want  lipgloss.Style
	}{
		{"below warn threshold", 0.5, styles.Fill},
		{"at warn threshold", 0.7, styles.Warn},
		{"above warn threshold", 0.9, styles.Warn},
		{"at limit", 1.0, styles.Bad},
		{"over limit", 1.2, styles.Bad},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fillStyleFor(tt.ratio, styles)
			if got.GetForeground() != tt.want.GetForeground() {
				t.Fatalf("fillStyleFor(%v) foreground = %v, want %v", tt.ratio, got.GetForeground(), tt.want.GetForeground())
			}
		})
	}
}

func TestMiniBarZeroWidth(t *testing.T) {
	t.Parallel()
	if got := MiniBar(1, 10, 0, BarStyles{}); got != "" {
		t.Fatalf("expected empty string for zero width, got %q", got)
	}
}
