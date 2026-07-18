package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func TestCardFillsWidthAndHeight(t *testing.T) {
	t.Parallel()
	got := Card("403 Forbidden\ncannot list secrets", lipgloss.NewStyle().Padding(0, 2), 40, 10)
	lines := strings.Split(got, "\n")
	if len(lines) != 10 {
		t.Fatalf("got %d lines, want 10", len(lines))
	}
	for i, l := range lines {
		if w := runewidth.StringWidth(l); w != 40 {
			t.Fatalf("line %d width = %d, want 40: %q", i, w, l)
		}
	}
}

func TestCardCentersContent(t *testing.T) {
	t.Parallel()
	got := Card("hi", lipgloss.NewStyle(), 20, 5)
	if !strings.Contains(got, "hi") {
		t.Fatalf("expected content in output:\n%s", got)
	}
	lines := strings.Split(got, "\n")
	found := false
	for _, l := range lines {
		if strings.Contains(l, "hi") {
			found = true
			// Bordered content roughly centered horizontally: neither
			// flush-left nor flush-right.
			idx := strings.Index(l, "hi")
			if idx == 0 || idx >= 15 {
				t.Fatalf("content not centered, index=%d: %q", idx, l)
			}
		}
	}
	if !found {
		t.Fatalf("content line not found:\n%s", got)
	}
}
