package yamlview

import (
	"fmt"
	"regexp"
)

// topLevelKeyPattern matches an unindented "key:" line — the block
// boundaries fold.go operates on (apiVersion, kind, metadata, spec,
// status, …).
var topLevelKeyPattern = regexp.MustCompile(`^([\w.\-/]+):`)

// block is one top-level key's span within the raw line list: [start, end)
// in 0-based line indices, start being the "key:" line itself.
type block struct {
	key   string
	start int
	end   int
}

// topLevelBlocks scans lines for indent-0 "key:" boundaries, each block
// running to the line before the next indent-0 key (or EOF).
func topLevelBlocks(lines []string) []block {
	var blocks []block
	for i, line := range lines {
		m := topLevelKeyPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if len(blocks) > 0 {
			blocks[len(blocks)-1].end = i
		}
		blocks = append(blocks, block{key: m[1], start: i, end: len(lines)})
	}
	return blocks
}

// defaultFolds folds "status" by default when present with more than one
// child line (docs/design README.md §8a: "verbose status blocks collapse
// to one dim line" — the one block the spec calls out to start folded;
// every other top-level key starts expanded).
func defaultFolds(lines []string) map[string]bool {
	folds := map[string]bool{}
	for _, b := range topLevelBlocks(lines) {
		if b.key == "status" && b.end-b.start > 1 {
			folds[b.key] = true
		}
	}
	return folds
}

// renderLine is one displayed line: either a real source line (LineNo > 0)
// or a synthetic summary/content line inserted by a post-process step
// (LineNo == 0) — a fold summary (renderLines), managedFields' real content
// (applyManagedFields, managedfields.go), or a Secret data value
// (applySecretReveal, secret.go).
type renderLine struct {
	Text    string
	LineNo  int
	FoldKey string // non-empty on a fold-summary line: which key it summarizes

	// FoldableKey marks the expanded header of a block whose fold state
	// lives in the same `folded` map as topLevelBlocks entries but whose
	// content isn't backed by a real Model.lines line — topLevelBlocks has
	// nothing to find it by, so the caller (yamlview's tab handling) checks
	// this field before falling back to toggleFoldAtCursor. managedFields
	// is the only current user (managedfields.go).
	FoldableKey string

	// SecretKey/SecretRevealed are set by applySecretReveal (secret.go,
	// docs/design README.md §21a) on a Secret's data: entries — non-empty
	// SecretKey means this line (and, for a multi-line decoded value, the
	// synthetic lines right after it) is a masked-or-revealed data value,
	// not ordinary YAML, so view.go skips TokenizeLine for it.
	SecretKey      string
	SecretRevealed bool
}

// renderLines produces the displayed line list from the raw source lines
// and fold state: folded blocks collapse to one summary line at the key's
// own position. managedFields and Secret data semantics are layered on top
// by applyManagedFields/applySecretReveal (Model.rendered chains them) since
// neither is backed by a real topLevelBlocks entry in lines.
func renderLines(lines []string, folded map[string]bool) []renderLine {
	blocks := topLevelBlocks(lines)
	blockAt := make(map[int]block, len(blocks))
	for _, b := range blocks {
		blockAt[b.start] = b
	}

	var out []renderLine
	for i := 0; i < len(lines); i++ {
		if b, ok := blockAt[i]; ok && folded[b.key] {
			out = append(out, renderLine{
				Text:    fmt.Sprintf("▸ %s (%d lines folded)", b.key, b.end-b.start-1),
				FoldKey: b.key,
			})
			i = b.end - 1
			continue
		}
		out = append(out, renderLine{Text: lines[i], LineNo: i + 1})
	}
	return out
}

// toggleFoldAtCursor flips the fold state of the block the rendered line at
// cursor belongs to: a fold-summary line unfolds its key; a real top-level
// key line folds it. Any other cursor position (including a FoldableKey
// header, which the caller checks first) is a no-op. Returns the possibly-
// updated map (folded is mutated in place when non-nil).
func toggleFoldAtCursor(lines []string, folded map[string]bool, rendered []renderLine, cursor int) {
	if cursor < 0 || cursor >= len(rendered) {
		return
	}
	rl := rendered[cursor]
	if rl.FoldKey != "" {
		folded[rl.FoldKey] = false
		return
	}
	if rl.LineNo == 0 {
		return
	}
	for _, b := range topLevelBlocks(lines) {
		if b.start == rl.LineNo-1 {
			if b.end-b.start > 1 {
				folded[b.key] = true
			}
			return
		}
	}
}

// unfoldAll clears every fold, including managedFields' — "f show all"
// per docs/design README.md §8a applies uniformly now that managedFields is
// a real (if separately-fetched) fold rather than a permanent placeholder.
func unfoldAll(folded map[string]bool) {
	for k := range folded {
		delete(folded, k)
	}
}
