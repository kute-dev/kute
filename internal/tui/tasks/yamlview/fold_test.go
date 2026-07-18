package yamlview

import (
	"strings"
	"testing"
)

var fixtureYAML = strings.Join([]string{
	"apiVersion: v1",
	"kind: Pod",
	"metadata:",
	"  name: worker-0",
	"  namespace: default",
	"spec:",
	"  containers:",
	"  - name: worker",
	"status:",
	"  phase: Running",
	"  conditions:",
	"  - type: Ready",
	"    status: \"True\"",
}, "\n")

func fixtureLines() []string { return strings.Split(fixtureYAML, "\n") }

func TestTopLevelBlocksBoundaries(t *testing.T) {
	blocks := topLevelBlocks(fixtureLines())
	want := []block{
		{key: "apiVersion", start: 0, end: 1},
		{key: "kind", start: 1, end: 2},
		{key: "metadata", start: 2, end: 5},
		{key: "spec", start: 5, end: 8},
		{key: "status", start: 8, end: 13},
	}
	if len(blocks) != len(want) {
		t.Fatalf("got %d blocks, want %d: %+v", len(blocks), len(want), blocks)
	}
	for i, b := range want {
		if blocks[i] != b {
			t.Fatalf("block[%d] = %+v, want %+v", i, blocks[i], b)
		}
	}
}

func TestDefaultFoldsFoldsMultiLineStatusOnly(t *testing.T) {
	folds := defaultFolds(fixtureLines())
	if !folds["status"] {
		t.Fatalf("expected status to be folded by default, got %+v", folds)
	}
	if len(folds) != 1 {
		t.Fatalf("expected only status folded by default, got %+v", folds)
	}
}

func TestDefaultFoldsLeavesOneLineStatusAlone(t *testing.T) {
	lines := []string{"apiVersion: v1", "status: Active"}
	folds := defaultFolds(lines)
	if folds["status"] {
		t.Fatalf("expected a one-line status to stay unfolded, got %+v", folds)
	}
}

func TestRenderLinesCollapsesFoldedBlockToOneSummary(t *testing.T) {
	lines := fixtureLines()
	folded := map[string]bool{"status": true}
	rendered := renderLines(lines, folded)

	var summary *renderLine
	for i := range rendered {
		if rendered[i].FoldKey == "status" {
			summary = &rendered[i]
		}
	}
	if summary == nil {
		t.Fatalf("expected a fold-summary line for status, got %+v", rendered)
	}
	if !strings.Contains(summary.Text, "4 lines folded") {
		t.Fatalf("expected the status block's 4 child lines counted, got %q", summary.Text)
	}
	for _, rl := range rendered {
		if strings.Contains(rl.Text, "phase: Running") {
			t.Fatalf("expected status's child lines to be collapsed away, found %q in %+v", rl.Text, rendered)
		}
	}
}

// managedFields' own fold/unfold behavior (applyManagedFields) is covered
// in managedfields_test.go — renderLines itself no longer knows about it.

func TestToggleFoldAtCursorFoldsAndUnfolds(t *testing.T) {
	lines := fixtureLines()
	folded := map[string]bool{}

	// Cursor on the "status:" line (index 8 in the raw lines == rendered
	// index 8, since nothing else is folded yet and there's no
	// managedFields synthetic line ahead of it).
	rendered := renderLines(lines, folded)
	cursor := -1
	for i, rl := range rendered {
		if rl.Text == "status:" {
			cursor = i
		}
	}
	if cursor == -1 {
		t.Fatalf("expected to find status: in rendered lines: %+v", rendered)
	}

	toggleFoldAtCursor(lines, folded, rendered, cursor)
	if !folded["status"] {
		t.Fatal("expected toggling on the status: line to fold it")
	}

	rendered = renderLines(lines, folded)
	summaryIdx := -1
	for i, rl := range rendered {
		if rl.FoldKey == "status" {
			summaryIdx = i
		}
	}
	if summaryIdx == -1 {
		t.Fatal("expected a fold-summary line after folding")
	}
	toggleFoldAtCursor(lines, folded, rendered, summaryIdx)
	if folded["status"] {
		t.Fatal("expected toggling on the fold-summary line to unfold it")
	}
}

func TestUnfoldAllClearsRealFoldsOnly(t *testing.T) {
	folded := map[string]bool{"status": true, "spec": true}
	unfoldAll(folded)
	if len(folded) != 0 {
		t.Fatalf("expected all real folds cleared, got %+v", folded)
	}
}
