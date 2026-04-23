package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestServer(t *testing.T) *server {
	t.Helper()
	log := newTestLogger()
	ctx := context.Background()
	s := &server{
		cfg: config{
			allowAnyOrigin:  true,
			rateLimitPerMin: 10000,
		},
		log:         log,
		fileStore:   NewFileStore(ctx, log),
		roomManager: NewRoomManager(ctx, log),
		rl:          newIPLimiter(10000),
	}
	s.upgrader = websocket.Upgrader{CheckOrigin: originChecker(s.cfg, log)}
	t.Cleanup(func() {
		s.fileStore.Shutdown()
		s.roomManager.Shutdown()
	})
	return s
}

func TestRoomManagerCreateAndGet(t *testing.T) {
	rm := NewRoomManager(context.Background(), newTestLogger())
	defer rm.Shutdown()
	room := rm.GetOrCreateRoom("abc123")
	if room == nil {
		t.Fatal("expected room, got nil")
	}
	if room.token != "abc123" {
		t.Fatalf("expected token abc123, got %s", room.token)
	}

	room2 := rm.GetOrCreateRoom("abc123")
	if room != room2 {
		t.Fatal("expected same room instance for same token")
	}
}

func TestRoomManagerDifferentTokens(t *testing.T) {
	rm := NewRoomManager(context.Background(), newTestLogger())
	defer rm.Shutdown()
	room1 := rm.GetOrCreateRoom("token1")
	room2 := rm.GetOrCreateRoom("token2")
	if room1 == room2 {
		t.Fatal("different tokens should create different rooms")
	}
}

func TestRoomManagerDelete(t *testing.T) {
	rm := NewRoomManager(context.Background(), newTestLogger())
	defer rm.Shutdown()
	rm.GetOrCreateRoom("delete-me")
	rm.DeleteRoom("delete-me")

	room := rm.GetOrCreateRoom("delete-me")
	if room == nil {
		t.Fatal("expected new room after delete")
	}
}

func TestRoomAddClientLimit(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/{token}", s.handleWebSocket)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:] + "/ws/limit-test"

	dialer := websocket.Dialer{}
	conn1, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first client failed to connect: %v", err)
	}
	defer conn1.Close()

	conn2, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("second client failed to connect: %v", err)
	}
	defer conn2.Close()

	conn3, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return
	}
	defer conn3.Close()

	_, _, err = conn3.ReadMessage()
	if err == nil {
		t.Fatal("third client should have been rejected")
	}
}

func TestRoomCleanup(t *testing.T) {
	rm := &RoomManager{rooms: make(map[string]*Room), log: newTestLogger()}

	rm.rooms["old"] = &Room{
		token:     "old",
		clients:   nil,
		createdAt: time.Now().Add(-15 * time.Minute),
	}
	rm.rooms["new"] = &Room{
		token:     "new",
		clients:   nil,
		createdAt: time.Now(),
	}

	rm.mu.Lock()
	for token, room := range rm.rooms {
		if time.Since(room.createdAt) > 10*time.Minute {
			delete(rm.rooms, token)
		}
	}
	rm.mu.Unlock()

	rm.mu.RLock()
	defer rm.mu.RUnlock()
	if _, exists := rm.rooms["old"]; exists {
		t.Fatal("expired room should have been cleaned up")
	}
	if _, exists := rm.rooms["new"]; !exists {
		t.Fatal("fresh room should still exist")
	}
}

func TestHealthEndpoint(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("health response not JSON: %v (%s)", err, w.Body.String())
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status=ok, got %v", body["status"])
	}
	if _, ok := body["version"]; !ok {
		t.Fatal("health missing version field")
	}
	if _, ok := body["protocol_version"]; !ok {
		t.Fatal("health missing protocol_version field")
	}
}

func TestUploadPageEndpoint(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /u/{token}", s.handleUploadPage)

	req := httptest.NewRequest("GET", "/u/testtoken", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("expected text/html content type, got %s", ct)
	}
}

func TestFileStoreRoundtrip(t *testing.T) {
	fs := NewFileStore(context.Background(), newTestLogger())
	defer fs.Shutdown()
	content := []byte("hello airpipe")

	token, err := fs.Store("test.txt", bytes.NewReader(content), "")
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if len(token) != 16 {
		t.Fatalf("expected 16 char token, got %d: %s", len(token), token)
	}

	sf, ok := fs.Get(token)
	if !ok {
		t.Fatal("file not found after store")
	}
	if sf.Filename != "test.txt" {
		t.Fatalf("expected filename test.txt, got %s", sf.Filename)
	}
	if sf.Size != int64(len(content)) {
		t.Fatalf("expected size %d, got %d", len(content), sf.Size)
	}
}

func TestFileStoreNotFound(t *testing.T) {
	fs := NewFileStore(context.Background(), newTestLogger())
	defer fs.Shutdown()
	_, ok := fs.Get("nonexistent")
	if ok {
		t.Fatal("expected not found for nonexistent token")
	}
}

func TestUploadAndDownload(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload", s.handleUploadFile)
	mux.HandleFunc("GET /d/{token}", s.handleDownloadPage)
	mux.HandleFunc("GET /raw/{token}", s.handleRawDownload)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("file", "encrypted.bin")
	part.Write([]byte("ciphertext-bytes"))
	mw.Close()

	resp, err := http.Post(srv.URL+"/upload", mw.FormDataContentType(), body)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("upload returned %d", resp.StatusCode)
	}

	var result struct {
		Token    string `json:"token"`
		Filename string `json:"filename"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Token == "" {
		t.Fatal("empty token in response")
	}

	dlResp, err := http.Get(srv.URL + "/d/" + result.Token)
	if err != nil {
		t.Fatalf("download page request failed: %v", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != 200 {
		t.Fatalf("download page returned %d", dlResp.StatusCode)
	}

	ct := dlResp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html, got %s", ct)
	}

	rawResp, err := http.Get(srv.URL + "/raw/" + result.Token)
	if err != nil {
		t.Fatalf("raw download request failed: %v", err)
	}
	defer rawResp.Body.Close()

	if rawResp.StatusCode != 200 {
		t.Fatalf("raw download returned %d", rawResp.StatusCode)
	}

	downloaded, _ := io.ReadAll(rawResp.Body)
	if string(downloaded) != "ciphertext-bytes" {
		t.Fatalf("expected 'ciphertext-bytes', got %q", string(downloaded))
	}
}

func TestDownloadNotFound(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /d/{token}", s.handleDownloadPage)
	mux.HandleFunc("GET /raw/{token}", s.handleRawDownload)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/d/nonexistent")
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}

	rawResp, _ := http.Get(srv.URL + "/raw/nonexistent")
	if rawResp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", rawResp.StatusCode)
	}
}

func TestOriginAllowlist(t *testing.T) {
	log := newTestLogger()
	cfg := config{
		allowedOrigins: []string{"https://airpipe.sanyamgarg.com"},
	}
	check := originChecker(cfg, log)

	cases := []struct {
		origin string
		allow  bool
	}{
		{"", true}, // CLI clients (no Origin header)
		{"https://airpipe.sanyamgarg.com", true},
		{"https://AIRPIPE.SANYAMGARG.COM", true},
		{"https://evil.example.com", false},
		{"http://airpipe.sanyamgarg.com", false}, // scheme mismatch
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/ws/x", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := check(r); got != c.allow {
			t.Errorf("origin %q: got allow=%v, want %v", c.origin, got, c.allow)
		}
	}
}

func TestRateLimit(t *testing.T) {
	log := newTestLogger()
	il := newIPLimiter(2) // 2 per minute = tight burst
	handler := rateLimit(il, log, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	call := func() int {
		r := httptest.NewRequest("GET", "/upload", nil)
		r.RemoteAddr = "1.2.3.4:5678"
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Code
	}

	if code := call(); code != 200 {
		t.Fatalf("first call should pass, got %d", code)
	}
	if code := call(); code != 200 {
		t.Fatalf("second call should pass (burst=2), got %d", code)
	}
	if code := call(); code != 429 {
		t.Fatalf("third call should be rate-limited, got %d", code)
	}
}
