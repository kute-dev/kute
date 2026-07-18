package yamlview

import (
	"regexp"
	"strings"
)

// TokenClass classifies one span of a tokenized YAML line for view.go's
// Theme-mapping (docs/design README.md §8a: "keys #c98fde, string values
// #b8d78f, punctuation #55556e, numbers/warn values #e8c74a"). Pure text
// in, tokens out — no Theme/lipgloss dependency, so highlight_test.go can
// assert on Token values directly rather than ANSI output.
type TokenClass int

const (
	Plain TokenClass = iota
	Key
	String
	Number
	Punct
	Comment
)

// Token is one styled span of a tokenized line.
type Token struct {
	Text  string
	Class TokenClass
}

// keyPattern matches a "key:" or "- key:" line start — a bare word/
// dotted/dashed identifier immediately followed by a colon (optionally
// preceded by a "- " list marker and leading whitespace). Quoted keys
// aren't handled (rare in kubectl-shaped YAML) — they fall through to the
// generic non-key tokenization below.
var keyPattern = regexp.MustCompile(`^(\s*)(- )?([\w.\-/]+):(\s.*)?$`)

// numberPattern matches a bare integer/float value (docs/design README.md's
// "numbers/warn values" — reuses Theme.Warn, there's no separate YamlNum
// token).
var numberPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// TokenizeLine classifies one raw YAML line into styled spans: leading
// indent (Plain), an optional "- " list marker (Punct), a "key:" pair
// (Key + Punct), and the value (String for quoted text, Number for
// numeric/true/false/null, Plain otherwise). A "#" comment consumes the
// rest of the line as Comment. Multi-line block scalars (|/>) aren't
// parsed specially — their continuation lines simply don't match
// keyPattern and fall through to plain-value tokenization, which reads
// correctly even without scalar awareness.
func TokenizeLine(line string) []Token {
	if line == "" {
		return nil
	}

	trimmed := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(trimmed)]

	if strings.HasPrefix(trimmed, "#") {
		var tokens []Token
		if indent != "" {
			tokens = append(tokens, Token{Text: indent, Class: Plain})
		}
		return append(tokens, Token{Text: trimmed, Class: Comment})
	}

	if m := keyPattern.FindStringSubmatch(line); m != nil {
		_, marker, key, rest := m[1], m[2], m[3], m[4]
		var tokens []Token
		if indent != "" {
			tokens = append(tokens, Token{Text: indent, Class: Plain})
		}
		if marker != "" {
			tokens = append(tokens, Token{Text: marker, Class: Punct})
		}
		tokens = append(tokens, Token{Text: key, Class: Key}, Token{Text: ":", Class: Punct})
		if rest == "" {
			return tokens
		}
		return append(tokens, tokenizeValue(rest)...)
	}

	// No "key:" pattern — a bare list item, a block-scalar continuation
	// line, or similar. Split off a leading "- " marker if present, then
	// tokenize the remainder as a value.
	if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
		var tokens []Token
		if indent != "" {
			tokens = append(tokens, Token{Text: indent, Class: Plain})
		}
		tokens = append(tokens, Token{Text: "- ", Class: Punct})
		rest := strings.TrimPrefix(trimmed, "-")
		rest = strings.TrimPrefix(rest, " ")
		return append(tokens, tokenizeValue(rest)...)
	}

	return []Token{{Text: line, Class: Plain}}
}

// tokenizeValue classifies a bare value (everything after "key: " or
// "- "): a leading space is kept as Plain, a quoted string becomes one
// String token (quotes included), a numeric/true/false/null token becomes
// Number, anything else is Plain.
func tokenizeValue(rest string) []Token {
	trimmed := strings.TrimLeft(rest, " ")
	lead := rest[:len(rest)-len(trimmed)]
	if trimmed == "" {
		if lead == "" {
			return nil
		}
		return []Token{{Text: lead, Class: Plain}}
	}

	var tokens []Token
	if lead != "" {
		tokens = append(tokens, Token{Text: lead, Class: Plain})
	}

	if len(trimmed) >= 2 && (trimmed[0] == '"' || trimmed[0] == '\'') && trimmed[len(trimmed)-1] == trimmed[0] {
		return append(tokens, Token{Text: trimmed, Class: String})
	}

	switch trimmed {
	case "true", "false", "null", "~":
		return append(tokens, Token{Text: trimmed, Class: Number})
	}
	if numberPattern.MatchString(trimmed) {
		return append(tokens, Token{Text: trimmed, Class: Number})
	}

	return append(tokens, Token{Text: trimmed, Class: Plain})
}
