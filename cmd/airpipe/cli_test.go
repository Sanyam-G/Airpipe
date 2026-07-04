package main

import "testing"

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
