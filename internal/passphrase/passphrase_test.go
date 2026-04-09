package passphrase

import (
	"encoding/hex"
	"regexp"
	"testing"
)

func TestGenerate(t *testing.T) {
	phrase := Generate()
	// Format: "WORD WORD WORD WORD NN"
	pattern := regexp.MustCompile(`^[A-Z]+ [A-Z]+ [A-Z]+ [A-Z]+ \d{2}$`)
	if !pattern.MatchString(phrase) {
		t.Fatalf("Generate() = %q, doesn't match expected pattern", phrase)
	}
}

func TestGenerateUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		p := Generate()
		if seen[p] {
			t.Fatalf("Generate() produced duplicate: %q", p)
		}
		seen[p] = true
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"RIVER FALCON MARBLE 42", "RIVER FALCON MARBLE 42"},
		{"river falcon marble 42", "RIVER FALCON MARBLE 42"},
		{"  River  Falcon  Marble  42  ", "RIVER FALCON MARBLE 42"},
		{"river   falcon\tmarble\n42", "RIVER FALCON MARBLE 42"},
	}
	for _, tt := range tests {
		got := Normalize(tt.input)
		if got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDeriveTokenDeterministic(t *testing.T) {
	a := DeriveToken("RIVER FALCON MARBLE 42")
	b := DeriveToken("RIVER FALCON MARBLE 42")
	if a != b {
		t.Fatalf("DeriveToken not deterministic: %q != %q", a, b)
	}
	if len(a) != 16 {
		t.Fatalf("DeriveToken length = %d, want 16", len(a))
	}
	// Verify it's valid hex
	if _, err := hex.DecodeString(a); err != nil {
		t.Fatalf("DeriveToken not valid hex: %v", err)
	}
}

func TestDeriveKeyDeterministic(t *testing.T) {
	a := DeriveKey("RIVER FALCON MARBLE 42")
	b := DeriveKey("RIVER FALCON MARBLE 42")
	if a != b {
		t.Fatalf("DeriveKey not deterministic")
	}
}

func TestDeriveTokenNormalizes(t *testing.T) {
	a := DeriveToken("RIVER FALCON MARBLE 42")
	b := DeriveToken("river  falcon  marble  42")
	if a != b {
		t.Fatalf("DeriveToken should normalize: %q != %q", a, b)
	}
}

func TestDeriveKeyNormalizes(t *testing.T) {
	a := DeriveKey("RIVER FALCON MARBLE 42")
	b := DeriveKey("river  falcon  marble  42")
	if a != b {
		t.Fatalf("DeriveKey should normalize")
	}
}

func TestTokenAndKeyAreIndependent(t *testing.T) {
	token := DeriveToken("RIVER FALCON MARBLE 42")
	key := DeriveKey("RIVER FALCON MARBLE 42")
	keyHex := hex.EncodeToString(key[:8])
	if token == keyHex {
		t.Fatal("Token and key first 8 bytes should differ (different domain prefixes)")
	}
}

// Hardcoded test vector for cross-language verification.
// If this test breaks, the JS derivation in words.js/index.html must also be updated.
func TestDeriveTestVector(t *testing.T) {
	token := DeriveToken("RIVER FALCON MARBLE 42")
	key := DeriveKey("RIVER FALCON MARBLE 42")

	// Log the values so we can hardcode the JS test
	t.Logf("Test vector - Token: %s", token)
	t.Logf("Test vector - Key hex: %s", hex.EncodeToString(key[:]))
}

func TestDifferentPhrasesProduceDifferentTokens(t *testing.T) {
	a := DeriveToken("RIVER FALCON MARBLE 42")
	b := DeriveToken("OCEAN TIGER STORM 77")
	if a == b {
		t.Fatal("Different passphrases should produce different tokens")
	}
}

func TestWordlistSize(t *testing.T) {
	if len(wordlist) != 1024 {
		t.Fatalf("Wordlist size = %d, want 1024", len(wordlist))
	}
	seen := make(map[string]bool)
	for _, w := range wordlist {
		if seen[w] {
			t.Fatalf("Duplicate word in wordlist: %s", w)
		}
		seen[w] = true
	}
}
