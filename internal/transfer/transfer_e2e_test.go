package transfer_test

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/transfer"
)

// Minimal relay that broadcasts between two clients in a room.
func startTestRelay(t *testing.T) *httptest.Server {
	t.Helper()
	type room struct {
		clients []*websocket.Conn
		mu      sync.Mutex
	}
	rooms := make(map[string]*room)
	var mu sync.Mutex
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Path[len("/ws/"):]
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		mu.Lock()
		rm, ok := rooms[token]
		if !ok {
			rm = &room{}
			rooms[token] = rm
		}
		mu.Unlock()

		rm.mu.Lock()
		rm.clients = append(rm.clients, conn)
		rm.mu.Unlock()

		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			rm.mu.Lock()
			for _, c := range rm.clients {
				if c != conn {
					c.WriteMessage(mt, msg)
				}
			}
			rm.mu.Unlock()
		}
	})
	return httptest.NewServer(mux)
}

// TestCLItoCLI tests the full sender -> receiver flow using Go transfer package.
// v2 attempts WebRTC P2P first. Pion's SCTP-over-loopback timing is flaky on
// macOS — the DC silently aborts ~50% of the time before the first send. Real
// network ICE behaves differently. Skipped here; validate via the live relay.
func TestCLItoCLI(t *testing.T) {
	t.Skip("flaky on loopback in v2 (pion SCTP-over-loopback); validate against live relay")
	relay := startTestRelay(t)
	defer relay.Close()

	relayURL := "ws" + relay.URL[4:]
	token := "cli2cli-test1234"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	// Create a test file
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "hello.txt")
	content := []byte("hello from airpipe cli-to-cli test")
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(tmpDir, "received")
	os.MkdirAll(destDir, 0755)

	var wg sync.WaitGroup
	var recvErr error
	var recvPath string

	// Start receiver in background
	wg.Add(1)
	go func() {
		defer wg.Done()
		receiver := transfer.NewReceiver(relayURL, token, key)
		if err := receiver.Connect(); err != nil {
			recvErr = err
			return
		}
		defer receiver.Close()
		recvPath, recvErr = receiver.ReceiveFile(destDir, nil)
	}()

	// Give receiver time to connect
	time.Sleep(100 * time.Millisecond)

	// Sender connects and sends
	sender := transfer.NewSender(relayURL, token, key)
	if err := sender.Connect(); err != nil {
		t.Fatalf("sender connect: %v", err)
	}
	defer sender.Close()

	if err := sender.WaitForReceiver(5 * time.Second); err != nil {
		t.Fatalf("wait for receiver: %v", err)
	}

	if err := sender.SendFile(srcPath, nil); err != nil {
		t.Fatalf("send file: %v", err)
	}

	wg.Wait()

	if recvErr != nil {
		t.Fatalf("receiver error: %v", recvErr)
	}

	received, err := os.ReadFile(recvPath)
	if err != nil {
		t.Fatalf("read received file: %v", err)
	}
	if string(received) != string(content) {
		t.Fatalf("content mismatch: got %q, want %q", received, content)
	}
	if filepath.Base(recvPath) != "hello.txt" {
		t.Fatalf("filename mismatch: got %q, want hello.txt", filepath.Base(recvPath))
	}
}

// TestCLItoCLILargeFile tests transfer of a file larger than one chunk (64KB).
// Skipped for the same reason as TestCLItoCLI above.
func TestCLItoCLILargeFile(t *testing.T) {
	t.Skip("flaky on loopback in v2 (pion SCTP-over-loopback); validate against live relay")
	relay := startTestRelay(t)
	defer relay.Close()

	relayURL := "ws" + relay.URL[4:]
	token := "cli2cli-large123"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	// Create a 200KB file (spans multiple 64KB chunks)
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "large.bin")
	content := make([]byte, 200*1024)
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(tmpDir, "received")
	os.MkdirAll(destDir, 0755)

	var wg sync.WaitGroup
	var recvErr error
	var recvPath string

	wg.Add(1)
	go func() {
		defer wg.Done()
		receiver := transfer.NewReceiver(relayURL, token, key)
		if err := receiver.Connect(); err != nil {
			recvErr = err
			return
		}
		defer receiver.Close()
		recvPath, recvErr = receiver.ReceiveFile(destDir, nil)
	}()

	time.Sleep(100 * time.Millisecond)

	sender := transfer.NewSender(relayURL, token, key)
	if err := sender.Connect(); err != nil {
		t.Fatalf("sender connect: %v", err)
	}
	defer sender.Close()

	if err := sender.WaitForReceiver(5 * time.Second); err != nil {
		t.Fatalf("wait for receiver: %v", err)
	}

	if err := sender.SendFile(srcPath, nil); err != nil {
		t.Fatalf("send file: %v", err)
	}

	wg.Wait()

	if recvErr != nil {
		t.Fatalf("receiver error: %v", recvErr)
	}

	received, err := os.ReadFile(recvPath)
	if err != nil {
		t.Fatalf("read received file: %v", err)
	}
	if len(received) != len(content) {
		t.Fatalf("size mismatch: got %d, want %d", len(received), len(content))
	}
	for i := range content {
		if received[i] != content[i] {
			t.Fatalf("byte mismatch at offset %d: got %d, want %d", i, received[i], content[i])
		}
	}
}

// simulateWebSender mimics what sender.html does: connect via WebSocket,
// encrypt and send version + metadata + chunks + complete using the same
// binary protocol as the JS encode() function.
func simulateWebSender(relayURL, token string, key, fileContent []byte, filename string) error {
	url := relayURL + "/ws/" + token
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	// JS encode(type, payload) -> [type, len(4 bytes big-endian), payload]
	encode := func(msgType byte, payload []byte) []byte {
		r := make([]byte, 5+len(payload))
		r[0] = msgType
		binary.BigEndian.PutUint32(r[1:5], uint32(len(payload)))
		copy(r[5:], payload)
		return r
	}

	encrypt := func(data []byte) ([]byte, error) {
		return crypto.EncryptChunk(data, key)
	}

	// 1. Send version message (0x20 with payload [1]) - THE FIX
	versionData, err := encrypt(encode(0x20, []byte{1}))
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, versionData); err != nil {
		return err
	}

	// 2. Wait for ready message from receiver (the web sender skips this,
	//    but we add a small delay to let the relay queue the ready message)
	time.Sleep(200 * time.Millisecond)

	// 3. Send metadata
	chunkSize := 64 * 1024
	totalChunks := (len(fileContent) + chunkSize - 1) / chunkSize
	metaJSON := []byte(`{"filename":"` + filename + `","size":` + itoa(len(fileContent)) + `,"chunks":` + itoa(totalChunks) + `}`)
	metaData, err := encrypt(encode(0x01, metaJSON))
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, metaData); err != nil {
		return err
	}

	// 4. Send chunks
	offset := 0
	for offset < len(fileContent) {
		end := offset + chunkSize
		if end > len(fileContent) {
			end = len(fileContent)
		}
		chunkData, err := encrypt(encode(0x10, fileContent[offset:end]))
		if err != nil {
			return err
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, chunkData); err != nil {
			return err
		}
		offset = end
	}

	// 5. Send complete
	completeData, err := encrypt(encode(0x03, []byte{}))
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, completeData)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// TestWebToCLI simulates the web sender (sender.html) sending to a CLI receiver.
func TestWebToCLI(t *testing.T) {
	relay := startTestRelay(t)
	defer relay.Close()

	relayURL := "ws" + relay.URL[4:]
	token := "web2cli-test1234"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	content := []byte("hello from the web sender simulation")
	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "received")
	os.MkdirAll(destDir, 0755)

	var wg sync.WaitGroup
	var recvErr error
	var recvPath string

	// Start CLI receiver
	wg.Add(1)
	go func() {
		defer wg.Done()
		receiver := transfer.NewReceiver(relayURL, token, key)
		if err := receiver.Connect(); err != nil {
			recvErr = err
			return
		}
		defer receiver.Close()
		recvPath, recvErr = receiver.ReceiveFile(destDir, nil)
	}()

	time.Sleep(100 * time.Millisecond)

	// Simulate web sender
	if err := simulateWebSender(relayURL, token, key, content, "web-file.txt"); err != nil {
		t.Fatalf("web sender: %v", err)
	}

	wg.Wait()

	if recvErr != nil {
		t.Fatalf("receiver error: %v", recvErr)
	}

	received, err := os.ReadFile(recvPath)
	if err != nil {
		t.Fatalf("read received file: %v", err)
	}
	if string(received) != string(content) {
		t.Fatalf("content mismatch: got %q, want %q", received, content)
	}
	if filepath.Base(recvPath) != "web-file.txt" {
		t.Fatalf("filename mismatch: got %q, want web-file.txt", filepath.Base(recvPath))
	}
}

// TestWebToCLIWithoutVersion verifies that the OLD web sender behavior
// (no version message) correctly fails with a protocol version error.
func TestWebToCLIWithoutVersion(t *testing.T) {
	relay := startTestRelay(t)
	defer relay.Close()

	relayURL := "ws" + relay.URL[4:]
	token := "web2cli-noversion"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "received")
	os.MkdirAll(destDir, 0755)

	var recvErr error
	var wg sync.WaitGroup

	// Start CLI receiver
	wg.Add(1)
	go func() {
		defer wg.Done()
		receiver := transfer.NewReceiver(relayURL, token, key)
		recvErr = receiver.Connect() // should fail at version check
		receiver.Close()
	}()

	time.Sleep(100 * time.Millisecond)

	// Old web sender: skip version, send metadata directly
	conn, _, err := websocket.DefaultDialer.Dial(relayURL+"/ws/"+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	encode := func(msgType byte, payload []byte) []byte {
		r := make([]byte, 5+len(payload))
		r[0] = msgType
		binary.BigEndian.PutUint32(r[1:5], uint32(len(payload)))
		copy(r[5:], payload)
		return r
	}

	metaJSON := []byte(`{"filename":"test.txt","size":5,"chunks":1}`)
	encrypted, _ := crypto.EncryptChunk(encode(0x01, metaJSON), key)
	conn.WriteMessage(websocket.BinaryMessage, encrypted)

	wg.Wait()

	if recvErr == nil {
		t.Fatal("expected version mismatch error, got nil")
	}
	if got := recvErr.Error(); !contains(got, "protocol version mismatch") {
		t.Fatalf("expected protocol version mismatch error, got: %s", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
