package mailbox_test

import (
	"bytes"
	"testing"

	"github.com/sanyamgarg/airpipe/internal/mailbox"
)

func TestEncodeDecodeV1(t *testing.T) {
	got, err := mailbox.EncodeV1("hello.txt", []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := mailbox.Decode(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "hello.txt" || string(entries[0].Content) != "body" {
		t.Fatalf("got %+v", entries)
	}
}

func TestEncodeDecodeAMB2(t *testing.T) {
	plain, err := mailbox.EncodeAMB2([]mailbox.Entry{
		{Name: "a.txt", Content: []byte("aa")},
		{Name: "b.bin", Content: []byte("bbb")},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := mailbox.Decode(plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Name != "a.txt" || out[1].Name != "b.bin" ||
		!bytes.Equal(out[0].Content, []byte("aa")) ||
		!bytes.Equal(out[1].Content, []byte("bbb")) {
		t.Fatalf("%+v", out)
	}
}
