package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRoomManagerCreateAndGet(t *testing.T) {
	rm := NewRoomManager()
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
	rm := NewRoomManager()
	room1 := rm.GetOrCreateRoom("token1")
	room2 := rm.GetOrCreateRoom("token2")
	if room1 == room2 {
		t.Fatal("different tokens should create different rooms")
	}
}

func TestRoomManagerDelete(t *testing.T) {
	rm := NewRoomManager()
	rm.GetOrCreateRoom("delete-me")
	rm.DeleteRoom("delete-me")

	room := rm.GetOrCreateRoom("delete-me")
	if room == nil {
		t.Fatal("expected new room after delete")
	}
}

func TestRoomAddClientLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/{token}", handleWebSocket)
	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := "ws" + server.URL[4:] + "/ws/limit-test"

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
	rm := &RoomManager{rooms: make(map[string]*Room)}

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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	})

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != `{"status":"ok"}` {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestUploadPageEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /u/{token}", handleUploadPage)

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
	fs := NewFileStore()
	content := []byte("hello airpipe")

	token, err := fs.Store("test.txt", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if len(token) != 6 {
		t.Fatalf("expected 6 char token, got %d: %s", len(token), token)
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
	fs := NewFileStore()
	_, ok := fs.Get("nonexistent")
	if ok {
		t.Fatal("expected not found for nonexistent token")
	}
}

func TestUploadAndDownload(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload", handleUploadFile)
	mux.HandleFunc("GET /d/{token}", handleDownload)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Upload
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("file", "hello.txt")
	part.Write([]byte("hello world"))
	mw.Close()

	resp, err := http.Post(server.URL+"/upload", mw.FormDataContentType(), body)
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
	if result.Filename != "hello.txt" {
		t.Fatalf("expected filename hello.txt, got %s", result.Filename)
	}

	// Download
	dlResp, err := http.Get(server.URL + "/d/" + result.Token)
	if err != nil {
		t.Fatalf("download request failed: %v", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != 200 {
		t.Fatalf("download returned %d", dlResp.StatusCode)
	}

	cd := dlResp.Header.Get("Content-Disposition")
	if cd != `attachment; filename="hello.txt"` {
		t.Fatalf("unexpected Content-Disposition: %s", cd)
	}

	downloaded, _ := io.ReadAll(dlResp.Body)
	if string(downloaded) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", string(downloaded))
	}
}

func TestDownloadNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /d/{token}", handleDownload)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, _ := http.Get(server.URL + "/d/nonexistent")
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
