// Secret semantics 8a grows when the viewed object is a Secret (docs/design
// README.md §21a): "data" values are never shown as raw base64. Parsing
// happens once per load over the already-rendered YAML text (no second
// fetch, no typed corev1.Secret dependency) so this stays a pure
// text-in/renderLine-out transform like fold.go and highlight.go.
package yamlview

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

// secretMaskGlyph is the fixed placeholder — length is not proportional to
// the real value's length, so it can never leak size-by-eye beyond the
// explicit "N B" figure next to it.
const secretMaskGlyph = "••••••••"

// secretDataLine is one parsed child entry of the top-level "data:" block.
type secretDataLine struct {
	key      string
	lineNo   int // 1-based index into Model.lines — matches renderLine.LineNo
	decoded  []byte
	decodeOK bool
}

// secretDataChildPattern matches a direct (2-space-indented) child of the
// top-level "data:" block — Secret.Data is always a flat map, so one indent
// level is all this needs to handle.
var secretDataChildPattern = regexp.MustCompile(`^  ([\w.\-/]+): (.*)$`)

// parseSecretData walks the "data:" top-level block (if any) and returns its
// direct child entries in source order. Nested/multi-line values under a key
// (rare for Secret.Data, which is always a flat map[string][]byte) aren't
// matched and are simply left out of the masked/reveal treatment.
func parseSecretData(lines []string) []secretDataLine {
	var out []secretDataLine
	for _, b := range topLevelBlocks(lines) {
		if b.key != "data" {
			continue
		}
		for i := b.start + 1; i < b.end && i < len(lines); i++ {
			m := secretDataChildPattern.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			key, raw := m[1], strings.Trim(m[2], `"'`)
			decoded, err := base64.StdEncoding.DecodeString(raw)
			out = append(out, secretDataLine{key: key, lineNo: i + 1, decoded: decoded, decodeOK: err == nil})
		}
	}
	return out
}

// parseSecretType finds the top-level "type:" scalar, defaulting to Opaque
// (the type Kubernetes assigns Secrets that don't set one explicitly).
func parseSecretType(lines []string) string {
	for _, b := range topLevelBlocks(lines) {
		if b.key != "type" {
			continue
		}
		rest := strings.TrimPrefix(lines[b.start], b.key+":")
		rest = strings.TrimSpace(rest)
		if rest != "" {
			return strings.Trim(rest, `"'`)
		}
	}
	return "Opaque"
}

// applySecretReveal post-processes an already fold-rendered line list,
// replacing each data: entry's key line with either a masked placeholder or
// (when revealed[key]) its decoded plaintext — multi-line decoded values
// expand into an indented block, one renderLine per decoded line, reusing
// the fold idiom's synthetic-line shape. No-op when entries is empty (every
// non-Secret kind).
func applySecretReveal(rendered []renderLine, entries []secretDataLine, revealed map[string]bool) []renderLine {
	if len(entries) == 0 {
		return rendered
	}
	byLine := make(map[int]secretDataLine, len(entries))
	for _, e := range entries {
		byLine[e.lineNo] = e
	}

	out := make([]renderLine, 0, len(rendered))
	for _, rl := range rendered {
		e, ok := byLine[rl.LineNo]
		if !ok {
			out = append(out, rl)
			continue
		}
		indent := rl.Text[:len(rl.Text)-len(strings.TrimLeft(rl.Text, " "))]
		if !revealed[e.key] {
			out = append(out, renderLine{
				Text:      fmt.Sprintf("%s%s: %s", indent, e.key, maskedPlaceholder(e)),
				LineNo:    rl.LineNo,
				SecretKey: e.key,
			})
			continue
		}
		out = append(out, revealedLines(indent, rl.LineNo, e)...)
	}
	return out
}

// maskedPlaceholder is 21a's "•••••••• · base64 · 41 B" — the byte count is
// the decoded plaintext size, never the base64 string's own length.
func maskedPlaceholder(e secretDataLine) string {
	n := len(e.decoded)
	if !e.decodeOK {
		return "invalid base64"
	}
	return fmt.Sprintf("%s · base64 · %d B", secretMaskGlyph, n)
}

// revealedLines renders one entry's decoded plaintext: a single line for
// single-line values, or a "key: |" header plus one indented renderLine per
// decoded line for multi-line values (certs, kubeconfigs, …) — "the fold
// idiom" per §21a, without actually being foldable (there's nothing to
// re-mask except the whole entry via 'x').
func revealedLines(indent string, lineNo int, e secretDataLine) []renderLine {
	if !e.decodeOK {
		return []renderLine{{Text: indent + e.key + ": (invalid base64)", LineNo: lineNo, SecretKey: e.key, SecretRevealed: true}}
	}
	text := strings.TrimRight(string(e.decoded), "\n")
	if !strings.Contains(text, "\n") {
		return []renderLine{{Text: indent + e.key + ": " + text, LineNo: lineNo, SecretKey: e.key, SecretRevealed: true}}
	}
	out := []renderLine{{Text: indent + e.key + ": |", LineNo: lineNo, SecretKey: e.key, SecretRevealed: true}}
	for _, l := range strings.Split(text, "\n") {
		out = append(out, renderLine{Text: indent + "  " + l, SecretKey: e.key, SecretRevealed: true})
	}
	return out
}
