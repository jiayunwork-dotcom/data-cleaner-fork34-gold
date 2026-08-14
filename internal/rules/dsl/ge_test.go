package dsl

import "testing"

func TestLexer_GreaterEqualIsOneToken(t *testing.T) {
	tokens, err := NewLexer("x >= 10").Tokenize()
	if err != nil {
		t.Fatalf("tokenize: %v", err)
	}
	found := false
	for _, tok := range tokens {
		if tok.Type == TokenGE {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a single >= token, got %#v", tokens)
	}
}
