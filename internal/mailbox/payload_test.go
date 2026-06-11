package mailbox_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/sanyamgarg/airpipe/internal/mailbox"
)

// Canonical fixtures pinning the wire format. The same hex strings live in
// cmd/relay/static/_amb2-check.html so the JS side asserts byte-equal output.
const fixtureV1Hex = "0000000968656c6c6f2e747874776f726c64"

var fixtureV1Entry = mailbox.Entry{Name: "hello.txt", Content: []byte("world")}

const fixtureAMB2Hex = "414d42320000000300000005612e747874000000000000000568656c6c6f00000008646174612e62696e0000000000000004010203ff00000009636166c3a92e747874000000000000000bc3bc6ec4b163c3b664c3a9"

var fixtureAMB2Entries = []mailbox.Entry{
	{Name: "a.txt", Content: []byte("hello")},
	{Name: "data.bin", Content: []byte{0x01, 0x02, 0x03, 0xff}},
	{Name: "café.txt", Content: []byte("ünıcödé")},
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncodeV1MatchesFixture(t *testing.T) {
	got, err := mailbox.EncodeV1(fixtureV1Entry.Name, fixtureV1Entry.Content)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != fixtureV1Hex {
		t.Fatalf("encoded mismatch\nwant %s\ngot  %s", fixtureV1Hex, hex.EncodeToString(got))
	}
}

func TestDecodeV1MatchesFixture(t *testing.T) {
	entries, err := mailbox.Decode(mustHex(t, fixtureV1Hex))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries: %d", len(entries))
	}
	if entries[0].Name != fixtureV1Entry.Name {
		t.Fatalf("name: %q", entries[0].Name)
	}
	if !bytes.Equal(entries[0].Content, fixtureV1Entry.Content) {
		t.Fatalf("content: %q", entries[0].Content)
	}
}

func TestEncodeAMB2MatchesFixture(t *testing.T) {
	got, err := mailbox.EncodeAMB2(fixtureAMB2Entries)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != fixtureAMB2Hex {
		t.Fatalf("encoded mismatch\nwant %s\ngot  %s", fixtureAMB2Hex, hex.EncodeToString(got))
	}
}

func TestDecodeAMB2MatchesFixture(t *testing.T) {
	entries, err := mailbox.Decode(mustHex(t, fixtureAMB2Hex))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(fixtureAMB2Entries) {
		t.Fatalf("entries: got %d want %d", len(entries), len(fixtureAMB2Entries))
	}
	for i, want := range fixtureAMB2Entries {
		if entries[i].Name != want.Name {
			t.Fatalf("entry %d name: got %q want %q", i, entries[i].Name, want.Name)
		}
		if !bytes.Equal(entries[i].Content, want.Content) {
			t.Fatalf("entry %d content: got %q want %q", i, entries[i].Content, want.Content)
		}
	}
}

func TestEncodeAMB2RejectsEmptyFilename(t *testing.T) {
	_, err := mailbox.EncodeAMB2([]mailbox.Entry{{Name: "", Content: []byte("x")}})
	if err == nil {
		t.Fatal("expected error for empty filename")
	}
	if !strings.Contains(err.Error(), "invalid filename") {
		t.Fatalf("error: %v", err)
	}
}

func TestEncodeV1RejectsEmptyFilename(t *testing.T) {
	_, err := mailbox.EncodeV1("", []byte("x"))
	if err == nil {
		t.Fatal("expected error for empty filename")
	}
}
