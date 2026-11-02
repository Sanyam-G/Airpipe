package passphrase

import (
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// Generate returns a passphrase like "DUSK PINE ECHO GUST 42".
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

// KDF parameters. Changing any of them changes every token and key, which
// breaks compatibility with other clients: bump transfer.ProtocolVersion too.
const (
	kdfSalt       = "airpipe/v5/kdf"
	KDFIterations = 600_000
)

// Secrets is everything a transfer needs from its passphrase.
type Secrets struct {
	Token        string   // room and mailbox token for a sender's transfer
	ReceiveToken string   // room token an `airpipe receive` waits on
	Key          [32]byte // NaCl secretbox key
}

// Derive runs PBKDF2-HMAC-SHA256 once over the normalized passphrase, then
// splits the result with HKDF labels so the tokens the relay sees reveal
// nothing about the key beyond the passphrase itself. It is slow on purpose:
// call it once per transfer and keep the result.
func Derive(phrase string) Secrets {
	master, err := pbkdf2.Key(sha256.New, Normalize(phrase), []byte(kdfSalt), KDFIterations, 32)
	if err != nil {
		panic("pbkdf2: " + err.Error())
	}
	expand := func(label string, n int) []byte {
		b, err := hkdf.Key(sha256.New, master, nil, label, n)
		if err != nil {
			panic("hkdf: " + err.Error())
		}
		return b
	}
	var s Secrets
	copy(s.Key[:], expand("airpipe/v5/key", 32))
	s.Token = hex.EncodeToString(expand("airpipe/v5/token", 16))
	s.ReceiveToken = hex.EncodeToString(expand("airpipe/v5/receive-token", 16))
	return s
}
