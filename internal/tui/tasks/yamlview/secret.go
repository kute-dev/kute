// Secret semantics 8a grows when the viewed object is a Secret (docs/design
// README.md §21a): no Secret value is ever on screen unless the user reveals
// it. Masking is structural, not a pass over the rendered text: the loaded
// YAML is parsed back into an object, every data/stringData value is swapped
// for an opaque token and the last-applied-configuration annotation (which
// embeds the whole manifest, data and stringData included) is redacted, and
// only then is it marshalled for display. Nothing the user didn't reveal can
// reach the line list — not through a key YAML happens to quote ("true",
// "1", "on"), not through an annotation, not through '/' search. The parse
// works on whatever the YAMLReader seam returned (the live cache, or a saved
// Helm manifest), so no typed corev1.Secret dependency is needed.
package yamlview

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

// secretMaskGlyph is the fixed placeholder — length is not proportional to
// the real value's length, so it can never leak size-by-eye beyond the
// explicit "N B" figure next to it.
const secretMaskGlyph = "••••••••"

// lastAppliedAnnotation is kubectl's client-side-apply record: the full
// manifest as last applied, so on a Secret it carries every value — base64
// data or plaintext stringData.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// secretMaskToken is the opaque stand-in a data/stringData value is marshalled
// as. It is plain [a-z0-9-] so YAML never quotes it, and it is how a value's
// rendered line is found again regardless of how YAML rendered the key.
const secretMaskToken = "kute-masked-secret-value-"

// secretUnparseableNotice is the whole body when a Secret's YAML can't be
// parsed back into an object: fail closed, never fall back to the raw text.
const secretUnparseableNotice = "# this Secret could not be parsed for masking — its contents are not shown"

// secretDataLine is one data/stringData entry of the viewed Secret.
type secretDataLine struct {
	// id is the reveal/copy identity: the key itself for data, and
	// "stringData/<key>" for stringData — Secret keys can't contain '/', so
	// the two sections can never collide.
	id string
	// label is the key exactly as YAML rendered it (`"true"` quoted).
	label   string
	section string
	lineNo  int // 1-based index into Model.lines — matches renderLine.LineNo
	decoded []byte
	// decodeOK is false for a data value that isn't valid base64.
	decodeOK bool
}

// maskedSecret is a Secret's YAML after maskSecretYAML.
type maskedSecret struct {
	// text is what the screen renders and searches: every value a token,
	// last-applied-configuration redacted.
	text string
	// copyText is 'Y''s full-YAML copy: data stays base64 (§21a), stringData
	// is folded into data as base64 (what the API server does with it), and
	// last-applied-configuration is dropped — so the copy carries no
	// plaintext either.
	copyText   string
	secretType string
	entries    []secretDataLine
}

// maskSecretYAML parses a Secret's YAML and returns its masked rendering plus
// the entries the reveal/copy keys work from. An error means the text could
// not be parsed as one object; the caller must then show nothing of it.
func maskSecretYAML(text string) (maskedSecret, error) {
	var obj map[string]any
	useNumber := func(d *json.Decoder) *json.Decoder { d.UseNumber(); return d }
	if err := sigsyaml.Unmarshal([]byte(text), &obj, useNumber); err != nil {
		return maskedSecret{}, err
	}
	if obj == nil {
		return maskedSecret{}, errors.New("empty Secret document")
	}

	out := maskedSecret{secretType: "Opaque"}
	if t, ok := obj["type"].(string); ok && t != "" {
		out.secretType = t
	}

	display := maps.Clone(obj)
	for _, section := range []string{"data", "stringData"} {
		values, ok := obj[section].(map[string]any)
		if !ok {
			if _, present := obj[section]; present && obj[section] != nil {
				// Not a flat map (malformed): drop it from display entirely
				// rather than render something we can't reason about.
				delete(display, section)
			}
			continue
		}
		masked := make(map[string]any, len(values))
		for _, key := range slices.Sorted(maps.Keys(values)) {
			e := secretDataLine{id: key, label: key, section: section}
			raw := scalarString(values[key])
			if section == "stringData" {
				e.id = "stringData/" + key
				e.decoded, e.decodeOK = []byte(raw), true
			} else {
				decoded, err := base64.StdEncoding.DecodeString(raw)
				e.decoded, e.decodeOK = decoded, err == nil
			}
			masked[key] = fmt.Sprintf("%s%d", secretMaskToken, len(out.entries))
			out.entries = append(out.entries, e)
		}
		display[section] = masked
	}
	display["metadata"] = redactLastApplied(obj["metadata"], true)

	data, err := sigsyaml.Marshal(display)
	if err != nil {
		return maskedSecret{}, err
	}
	out.text = string(data)
	locateSecretLines(strings.Split(out.text, "\n"), out.entries)

	copyObj := maps.Clone(obj)
	copyObj["metadata"] = redactLastApplied(obj["metadata"], false)
	if sd, ok := obj["stringData"].(map[string]any); ok {
		merged := map[string]any{}
		if d, ok := obj["data"].(map[string]any); ok {
			maps.Copy(merged, d)
		}
		for k, v := range sd {
			merged[k] = base64.StdEncoding.EncodeToString([]byte(scalarString(v)))
		}
		copyObj["data"] = merged
	}
	delete(copyObj, "stringData")
	if c, err := sigsyaml.Marshal(copyObj); err == nil {
		out.copyText = string(c)
	}
	return out, nil
}

// redactLastApplied returns a copy of metadata with the last-applied
// annotation replaced by a size-only placeholder (placeholder=true) or
// removed (placeholder=false). The input is never mutated.
func redactLastApplied(metadata any, placeholder bool) any {
	md, ok := metadata.(map[string]any)
	if !ok {
		return metadata
	}
	anns, ok := md["annotations"].(map[string]any)
	if !ok {
		return metadata
	}
	v, ok := anns[lastAppliedAnnotation]
	if !ok {
		return metadata
	}
	md = maps.Clone(md)
	anns = maps.Clone(anns)
	if placeholder {
		anns[lastAppliedAnnotation] = fmt.Sprintf("%s · contains secret data · %d B", secretMaskGlyph, len(scalarString(v)))
	} else {
		delete(anns, lastAppliedAnnotation)
	}
	if len(anns) == 0 {
		delete(md, "annotations")
	} else {
		md["annotations"] = anns
	}
	return md
}

// locateSecretLines fills each entry's lineNo and label from the masked
// text, finding its line by the token marshalled in place of its value. An
// entry whose line isn't found keeps lineNo 0 and simply shows its token —
// never its value.
func locateSecretLines(lines []string, entries []secretDataLine) {
	for i := range entries {
		suffix := fmt.Sprintf(": %s%d", secretMaskToken, i)
		for n, line := range lines {
			if !strings.HasSuffix(line, suffix) {
				continue
			}
			entries[i].lineNo = n + 1
			if label := strings.TrimSpace(strings.TrimSuffix(line, suffix)); label != "" {
				entries[i].label = label
			}
			break
		}
	}
}

// scalarString renders a parsed YAML scalar as the string the API server
// would hold for it.
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(t); err != nil {
			return fmt.Sprint(t)
		}
		return strings.TrimRight(buf.String(), "\n")
	}
}

// applySecretReveal post-processes an already fold-rendered line list,
// replacing each entry's line with either a masked placeholder or (when
// revealed[id]) its plaintext — multi-line values expand into an indented
// block, one renderLine per line, reusing the fold idiom's synthetic-line
// shape. No-op when entries is empty (every non-Secret kind).
func applySecretReveal(rendered []renderLine, entries []secretDataLine, revealed map[string]bool) []renderLine {
	if len(entries) == 0 {
		return rendered
	}
	byLine := make(map[int]secretDataLine, len(entries))
	for _, e := range entries {
		if e.lineNo > 0 {
			byLine[e.lineNo] = e
		}
	}

	out := make([]renderLine, 0, len(rendered))
	for _, rl := range rendered {
		e, ok := byLine[rl.LineNo]
		if !ok || rl.LineNo == 0 {
			out = append(out, rl)
			continue
		}
		indent := rl.Text[:len(rl.Text)-len(strings.TrimLeft(rl.Text, " "))]
		if !revealed[e.id] {
			out = append(out, renderLine{
				Text:        fmt.Sprintf("%s%s: %s", indent, e.label, maskedPlaceholder(e)),
				LineNo:      rl.LineNo,
				SecretKey:   e.id,
				SecretLabel: e.label,
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
	if !e.decodeOK {
		return "invalid base64"
	}
	encoding := "base64"
	if e.section == "stringData" {
		encoding = "stringData"
	}
	return fmt.Sprintf("%s · %s · %d B", secretMaskGlyph, encoding, len(e.decoded))
}

// revealedLines renders one entry's plaintext: a single line for
// single-line values, or a "key: |" header plus one indented renderLine per
// line for multi-line values (certs, kubeconfigs, …) — "the fold idiom" per
// §21a, without actually being foldable (there's nothing to re-mask except
// the whole entry via 'x').
func revealedLines(indent string, lineNo int, e secretDataLine) []renderLine {
	head := renderLine{LineNo: lineNo, SecretKey: e.id, SecretLabel: e.label, SecretRevealed: true}
	if !e.decodeOK {
		head.Text = indent + e.label + ": (invalid base64)"
		return []renderLine{head}
	}
	text := strings.TrimRight(string(e.decoded), "\n")
	if !strings.Contains(text, "\n") {
		head.Text = indent + e.label + ": " + text
		return []renderLine{head}
	}
	head.Text = indent + e.label + ": |"
	out := []renderLine{head}
	for _, l := range strings.Split(text, "\n") {
		out = append(out, renderLine{Text: indent + "  " + l, SecretKey: e.id, SecretLabel: e.label, SecretRevealed: true})
	}
	return out
}
