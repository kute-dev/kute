// Package golden holds no code — only the .golden fixtures every screen's
// rendering test compares against. This one test is the exception: it is an
// invariant over the fixtures themselves rather than over any one screen.
package golden

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// dimsInName pulls the terminal size a fixture was rendered at out of its own
// filename — every fixture carries it (`120x36.golden`, `allns-80x24.golden`,
// `120x36-dark.golden`).
var dimsInName = regexp.MustCompile(`(\d+)x(\d+)`)

// TestGoldenLinesFitTheirDeclaredWidth asserts no line in any fixture is
// wider than the terminal the fixture claims to be.
//
// Nothing in the tree violates this today, so it is a regression guard, not
// a fix. It earns its place because two decided-but-unbuilt items — the
// ASCII glyph fallback and the minimum-terminal-size guard screen — both
// change how many cells a rendered row occupies, and the glyph fallback's
// acceptance criterion is already "no column-width drift, the goldens are
// the check". That is only true if something checks them: a per-screen
// golden comparison fails on *any* difference, which means a width
// regression is indistinguishable from an intended re-render, and the usual
// response to a failing golden is UPDATE_GOLDEN=1. This test is the one that
// keeps saying no afterwards.
//
// Display width, not len: the fixtures are full of box-drawing and status
// glyphs, and the ANSI-carrying themed fixtures need their escapes
// discounted — ansi.StringWidth does both.
func TestGoldenLinesFitTheirDeclaredWidth(t *testing.T) {
	t.Parallel()

	checked := 0
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".golden" {
			return nil
		}
		m := dimsInName.FindStringSubmatch(d.Name())
		if m == nil {
			// components/table's own fixtures render at a component width
			// with no terminal around them, so they declare no size and
			// there is nothing to check them against.
			return nil
		}
		width, convErr := strconv.Atoi(m[1])
		if convErr != nil {
			return convErr
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		checked++
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			for i, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
				if got := ansi.StringWidth(line); got > width {
					t.Errorf("line %d is %d cells wide, past the fixture's declared %d:\n%s",
						i+1, got, width, ansi.Strip(line))
				}
			}
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the fixtures: %v", err)
	}
	// A glob that silently matches nothing is the classic way for a test
	// like this to pass forever without looking at anything.
	if checked == 0 {
		t.Fatal("found no sized .golden fixtures to check")
	}
}
