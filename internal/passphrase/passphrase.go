package passphrase

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// Generate returns a passphrase like "RIVER FALCON MARBLE 42".
// 4 random words from the 1024-word list + a 2-digit number (10-99).
func Generate() string {
	words := make([]string, 4)
	for i := range words {
		n, err := rand.Int(rand.Reader, big.NewInt(1024))
		if err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		words[i] = wordlist[n.Int64()]
	}
	num, err := rand.Int(rand.Reader, big.NewInt(90))
	if err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("%s %d", strings.Join(words, " "), num.Int64()+10)
}

// Normalize uppercases, trims, and collapses whitespace so entry is forgiving.
func Normalize(phrase string) string {
	phrase = strings.ToUpper(strings.TrimSpace(phrase))
	fields := strings.Fields(phrase)
	return strings.Join(fields, " ")
}

// DeriveToken returns a 16-char hex token derived from the passphrase.
// Uses SHA-256 with a domain-separated prefix, takes the first 8 bytes.
func DeriveToken(phrase string) string {
	h := sha256.Sum256([]byte("airpipe:token:" + Normalize(phrase)))
	return hex.EncodeToString(h[:8])
}

// DeriveKey returns a 32-byte NaCl secretbox key derived from the passphrase.
// Uses SHA-256 with a domain-separated prefix.
func DeriveKey(phrase string) [32]byte {
	return sha256.Sum256([]byte("airpipe:key:" + Normalize(phrase)))
}
