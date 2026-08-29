package relay

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

func testConfig() Config {
	return Config{
		AllowAnyOrigin:  true,
		RateLimitPerMin: 10000,
		MaxUploadBytes:  500 << 20,
		FileExpiry:      10 * time.Minute,
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWithConfig(t, testConfig())
}

func newTestServerWithConfig(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := New(context.Background(), cfg, newTestLogger(), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	return s
}

func newTestFileStore(t *testing.T) *FileStore {
	t.Helper()
	fs, err := NewFileStore(context.Background(), newTestLogger(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fs.Shutdown)
	return fs
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
	srv := httptest.NewServer(s.Routes())
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
		token:        "old",
		clients:      nil,
		lastActivity: time.Now().Add(-15 * time.Minute),
	}
	rm.rooms["new"] = &Room{
		token:        "new",
		clients:      nil,
		lastActivity: time.Now(),
	}

	rm.mu.Lock()
	for token, room := range rm.rooms {
		if time.Since(room.lastActivity) > 10*time.Minute {
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

func TestRoomLastActivityRefresh(t *testing.T) {
	room := &Room{token: "t", clients: nil, lastActivity: time.Now().Add(-20 * time.Minute)}
	room.Broadcast(nil, []byte{})
	if time.Since(room.lastActivity) > time.Second {
		t.Fatalf("Broadcast did not refresh lastActivity: idle=%s", time.Since(room.lastActivity))
	}
}

func TestHealthEndpoint(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)

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
	if _, ok := body["max_upload_bytes"]; !ok {
		t.Fatal("health missing max_upload_bytes field")
	}
	if _, ok := body["expiry_seconds"]; !ok {
		t.Fatal("health missing expiry_seconds field")
	}
	if _, ok := body["active_files"]; ok {
		t.Fatal("health leaks stats without PublicStats")
	}
}

func TestMetricsHiddenByDefault(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404 without PublicStats, got %d", w.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	cfg := testConfig()
	cfg.PublicStats = true
	s := newTestServerWithConfig(t, cfg)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"airpipe_build_info",
		"airpipe_uptime_seconds",
		"airpipe_active_files",
		"airpipe_uploads_total",
		"airpipe_rate_limited_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}

func TestMinimalUILanding(t *testing.T) {
	cfg := testConfig()
	cfg.MinimalUI = true
	s := newTestServerWithConfig(t, cfg)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "pp-input") {
		t.Fatal("minimal landing missing receive box")
	}
}

func TestUploadPageEndpoint(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest("GET", "/u/testtoken", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("expected text/html content type, got %s", ct)
	}
}

func TestFileStoreRoundtrip(t *testing.T) {
	fs := newTestFileStore(t)
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
	fs := newTestFileStore(t)
	_, ok := fs.Get("nonexistent")
	if ok {
		t.Fatal("expected not found for nonexistent token")
	}
}

func TestUploadAndDownload(t *testing.T) {
	s := newTestServer(t)
	srv := httptest.NewServer(s.Routes())
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

	if got := s.uploadsTotal.Load(); got != 1 {
		t.Fatalf("uploadsTotal = %d, want 1", got)
	}
	if got := s.downloadsTotal.Load(); got != 1 {
		t.Fatalf("downloadsTotal = %d, want 1", got)
	}
}

func TestDownloadNotFound(t *testing.T) {
	s := newTestServer(t)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	// /d/{token} always returns 200; the page client-side probes /raw
	// and falls back to live WS pairing if the mailbox blob isn't present.
	resp, _ := http.Get(srv.URL + "/d/nonexistent")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	rawResp, _ := http.Get(srv.URL + "/raw/nonexistent")
	if rawResp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", rawResp.StatusCode)
	}
}

func TestOriginAllowlist(t *testing.T) {
	log := newTestLogger()
	cfg := Config{
		AllowedOrigins: []string{"https://airpipe.sanyamgarg.com"},
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
		{"http://example.com", true},             // same-origin: matches request Host
		{"http://localhost:8199", false},         // different host, not allowlisted
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/ws/x", nil) // Host: example.com
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := check(r); got != c.allow {
			t.Errorf("origin %q: got allow=%v, want %v", c.origin, got, c.allow)
		}
	}

	// Same-origin on a custom port.
	r := httptest.NewRequest("GET", "http://localhost:8199/ws/x", nil)
	r.Header.Set("Origin", "http://localhost:8199")
	if !check(r) {
		t.Error("same-origin on custom port should be allowed")
	}
}

func TestRateLimit(t *testing.T) {
	s := newTestServer(t)
	s.rl = newIPLimiter(2) // 2 per minute = tight burst
	handler := s.rateLimit(func(w http.ResponseWriter, r *http.Request) {
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
	if got := s.rateLimitedTotal.Load(); got != 1 {
		t.Fatalf("rateLimitedTotal = %d, want 1", got)
	}
}

func TestConfigEnvOverrides(t *testing.T) {
	t.Setenv("AIRPIPE_MAX_UPLOAD_MB", "100")
	t.Setenv("AIRPIPE_FILE_EXPIRY", "30m")
	t.Setenv("AIRPIPE_RATE_LIMIT_PER_MIN", "120")

	cfg := LoadConfig()
	if cfg.MaxUploadBytes != 100<<20 {
		t.Fatalf("MaxUploadBytes = %d, want %d", cfg.MaxUploadBytes, int64(100)<<20)
	}
	if cfg.FileExpiry != 30*time.Minute {
		t.Fatalf("FileExpiry = %s, want 30m", cfg.FileExpiry)
	}
	if cfg.RateLimitPerMin != 120 {
		t.Fatalf("RateLimitPerMin = %d, want 120", cfg.RateLimitPerMin)
	}
}

func TestConfigBadEnvFallsBack(t *testing.T) {
	t.Setenv("AIRPIPE_MAX_UPLOAD_MB", "not-a-number")
	t.Setenv("AIRPIPE_FILE_EXPIRY", "-5m")

	cfg := LoadConfig()
	if cfg.MaxUploadBytes != 500<<20 {
		t.Fatalf("MaxUploadBytes = %d, want default", cfg.MaxUploadBytes)
	}
	if cfg.FileExpiry != 10*time.Minute {
		t.Fatalf("FileExpiry = %s, want default 10m", cfg.FileExpiry)
	}
}

func TestRoomStatusEndpoint(t *testing.T) {
	s := newTestServer(t)
	token := "00112233aabbccdd"

	check := func(path string, wantStatus int, wantWaiting bool) {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.SetPathValue("token", path[len("/room/"):])
		w := httptest.NewRecorder()
		s.handleRoomStatus(w, req)
		if w.Code != wantStatus {
			t.Fatalf("%s: status %d, want %d", path, w.Code, wantStatus)
		}
		if wantStatus != http.StatusOK {
			return
		}
		var body struct {
			Waiting bool `json:"waiting"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Waiting != wantWaiting {
			t.Fatalf("%s: waiting %v, want %v", path, body.Waiting, wantWaiting)
		}
	}

	check("/room/"+token, http.StatusOK, false)

	room := s.roomManager.GetOrCreateRoom(token)
	room.AddClient(&websocket.Conn{})
	check("/room/"+token, http.StatusOK, true)

	room.AddClient(&websocket.Conn{})
	check("/room/"+token, http.StatusOK, false)

	check("/room/not-a-token", http.StatusBadRequest, false)

	s.roomManager.DeleteRoom(token)
}
