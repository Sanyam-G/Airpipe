package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sanyam-G/Airpipe/v5/internal/transfer"
)

func TestArchiveName(t *testing.T) {
	cases := []struct {
		paths []string
		want  string
	}{
		{[]string{"photos"}, "photos.zip"},
		{[]string{"photos/"}, "photos.zip"},
		{[]string{"/Users/x/docs"}, "docs.zip"},
		{[]string{"."}, "airpipe-1-items.zip"},
		{[]string{"a.txt", "b.txt"}, "airpipe-2-items.zip"},
		{[]string{"photos", "docs", "a.txt"}, "airpipe-3-items.zip"},
	}
	for _, c := range cases {
		if got := archiveName(c.paths); got != c.want {
			t.Errorf("archiveName(%v) = %q, want %q", c.paths, got, c.want)
		}
	}
}

func TestFmtBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KB"},
		{5 << 20, "5.0 MB"},
		{3 << 30, "3.00 GB"},
	}
	for _, c := range cases {
		if got := fmtBytes(c.in); got != c.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseStayOpenFlags(t *testing.T) {
	stay, rest := parseStayOpenFlags([]string{"--stay-open", "RIVER", "FALCON"})
	if !stay || len(rest) != 2 {
		t.Fatalf("got stay=%v rest=%v", stay, rest)
	}
	stay, rest = parseStayOpenFlags([]string{"RIVER", "FALCON"})
	if stay || len(rest) != 2 {
		t.Fatalf("got stay=%v rest=%v", stay, rest)
	}
}

func TestRelayURLConversion(t *testing.T) {
	if got := toWS("https://airpipe.example.com"); got != "wss://airpipe.example.com" {
		t.Fatal(got)
	}
	if got := toHTTP("wss://airpipe.example.com"); got != "https://airpipe.example.com" {
		t.Fatal(got)
	}
}

func TestCheckRelay(t *testing.T) {
	for _, c := range []struct {
		proto int
		ok    bool
	}{{int(transfer.ProtocolVersion), true}, {3, false}, {int(transfer.ProtocolVersion) + 1, false}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"version":"vX","protocol_version":%d}`, c.proto)
		}))
		err := checkRelay(srv.URL)
		srv.Close()
		if (err == nil) != c.ok {
			t.Errorf("protocol %d: err = %v, want ok=%v", c.proto, err, c.ok)
		}
	}
}

func TestDownloadReleaseVerifiesChecksum(t *testing.T) {
	const sumOfHello = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	var digest string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bin" {
			fmt.Fprint(w, "hello")
			return
		}
		fmt.Fprintf(w, `{"assets":[{"name":"airpipe-test","browser_download_url":"%s/bin","digest":"%s"}]}`, srv.URL, digest)
	}))
	defer srv.Close()

	digest = "sha256:" + sumOfHello
	if got, err := downloadRelease(srv.URL, "airpipe-test"); err != nil || string(got) != "hello" {
		t.Fatalf("matching checksum: %q, %v", got, err)
	}
	for _, bad := range []string{"sha256:" + sumOfHello[:63] + "0", ""} {
		digest = bad
		if _, err := downloadRelease(srv.URL, "airpipe-test"); err == nil {
			t.Fatalf("digest %q should be rejected", bad)
		}
	}
	if _, err := downloadRelease(srv.URL, "airpipe-other"); err == nil {
		t.Fatal("missing asset should be an error")
	}
}
