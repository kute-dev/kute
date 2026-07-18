package yamlview

import "testing"

func classesOf(tokens []Token) []TokenClass {
	out := make([]TokenClass, len(tokens))
	for i, t := range tokens {
		out[i] = t.Class
	}
	return out
}

func textOf(tokens []Token) string {
	s := ""
	for _, t := range tokens {
		s += t.Text
	}
	return s
}

func TestTokenizeLineKeyWithQuotedString(t *testing.T) {
	tokens := TokenizeLine(`  name: "worker-0"`)
	if textOf(tokens) != `  name: "worker-0"` {
		t.Fatalf("round-trip mismatch: %q", textOf(tokens))
	}
	var haveKey, haveString bool
	for _, tok := range tokens {
		if tok.Class == Key && tok.Text == "name" {
			haveKey = true
		}
		if tok.Class == String && tok.Text == `"worker-0"` {
			haveString = true
		}
	}
	if !haveKey || !haveString {
		t.Fatalf("expected Key %q and String %q tokens, got %+v", "name", `"worker-0"`, tokens)
	}
}

func TestTokenizeLineKeyWithNumber(t *testing.T) {
	tokens := TokenizeLine("  restartCount: 6")
	var haveNumber bool
	for _, tok := range tokens {
		if tok.Class == Number && tok.Text == "6" {
			haveNumber = true
		}
	}
	if !haveNumber {
		t.Fatalf("expected a Number token for '6', got %+v", tokens)
	}
}

func TestTokenizeLineBooleanAndNullValues(t *testing.T) {
	for _, line := range []string{"ready: true", "ready: false", "value: null"} {
		tokens := TokenizeLine(line)
		var haveNumber bool
		for _, tok := range tokens {
			if tok.Class == Number {
				haveNumber = true
			}
		}
		if !haveNumber {
			t.Fatalf("expected %q to classify its value as Number(warn), got %+v", line, tokens)
		}
	}
}

func TestTokenizeLineComment(t *testing.T) {
	tokens := TokenizeLine("  # this is a comment")
	found := false
	for _, tok := range tokens {
		if tok.Class == Comment && tok.Text == "# this is a comment" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the whole remainder to be one Comment token, got %+v", tokens)
	}
}

func TestTokenizeLineBareListItem(t *testing.T) {
	tokens := TokenizeLine("  - 10.0.0.1/24")
	if classesOf(tokens)[0] != Plain {
		t.Fatalf("expected leading indent as Plain, got %+v", tokens)
	}
	var havePunct bool
	for _, tok := range tokens {
		if tok.Class == Punct && tok.Text == "- " {
			havePunct = true
		}
	}
	if !havePunct {
		t.Fatalf("expected a '- ' Punct token, got %+v", tokens)
	}
	if textOf(tokens) != "  - 10.0.0.1/24" {
		t.Fatalf("round-trip mismatch: %q", textOf(tokens))
	}
}

func TestTokenizeLineKeyWithNestedMapHasNoValueTokens(t *testing.T) {
	tokens := TokenizeLine("metadata:")
	if len(tokens) != 2 {
		t.Fatalf("expected exactly Key+Punct for a bare 'key:' line, got %+v", tokens)
	}
	if tokens[0].Class != Key || tokens[0].Text != "metadata" {
		t.Fatalf("expected Key token 'metadata', got %+v", tokens[0])
	}
}

func TestTokenizeLineEmptyReturnsNil(t *testing.T) {
	if got := TokenizeLine(""); got != nil {
		t.Fatalf("expected nil for an empty line, got %+v", got)
	}
}
