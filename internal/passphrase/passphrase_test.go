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

// Known-answer vectors. The browser implementation (deriveSecrets in
// internal/relay/static/airpipe-protocol.js and the copy in web/assets/app.js)
// must produce the same values; /static/_kdf-check.html checks it in a browser.
var deriveVectors = []struct {
	phrase, token, receiveToken, key string
}{
	{"RIVER FALCON MARBLE 42", "95662f2cc42823ab1ff21ca9e81b886d", "f46edda0efefcd42cf1887ec5ab36df5", "43273724277857759978acee2bb0f0eb14773b45175f4783cadaa66588633791"},
	{"OCEAN TIGER STORM 77", "c7bbb4eb021f118a9603cbdaaa38995d", "a38edfee5691dae8d263d878dcf0c28a", "fda61134b1a26b913fee5625dc3300e6c1600f7f8133dd9d222141b34c81db67"},
}

func TestDeriveVectors(t *testing.T) {
	for _, v := range deriveVectors {
		s := Derive(v.phrase)
		if s.Token != v.token || s.ReceiveToken != v.receiveToken || hex.EncodeToString(s.Key[:]) != v.key {
			t.Errorf("Derive(%q) = %s %s %x, want %s %s %s", v.phrase, s.Token, s.ReceiveToken, s.Key, v.token, v.receiveToken, v.key)
		}
	}
}

func TestDeriveNormalizes(t *testing.T) {
	if Derive("  river  falcon\tmarble 42 ") != Derive("RIVER FALCON MARBLE 42") {
		t.Fatal("Derive should normalize the passphrase")
	}
}

func TestDeriveOutputsDistinct(t *testing.T) {
	s := Derive("RIVER FALCON MARBLE 42")
	key := hex.EncodeToString(s.Key[:])
	if s.Token == s.ReceiveToken || key[:32] == s.Token || key[:32] == s.ReceiveToken {
		t.Fatal("token, receive token and key must come from distinct labels")
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
