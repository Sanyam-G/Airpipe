package transfer_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/p2p"
	"github.com/sanyamgarg/airpipe/internal/transfer"
)

func writeOneFileWS(t *testing.T, write func(transfer.Message), content []byte, filename string) {
	t.Helper()
	const chunk = 64 * 1024
	meta, err := transfer.NewMetadataMessage(filename, int64(len(content)), (len(content)+chunk-1)/chunk)
	if err != nil {
		t.Fatal(err)
	}
	write(meta)
	for off := 0; off < len(content); off += chunk {
		end := off + chunk
		if end > len(content) {
			end = len(content)
		}
		write(transfer.NewChunkMessage(content[off:end]))
	}
	write(transfer.NewCompleteMessage())
}

func fakeSenderP2PFail(t *testing.T, conn *websocket.Conn, key, content []byte, filename string, sendOffer bool) {
	write := func(m transfer.Message) {
		ct, err := crypto.EncryptChunk(transfer.EncodeMessage(m), key)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, ct); err != nil {
			t.Fatal(err)
		}
	}

	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	if sendOffer {
		offerer, err := p2p.NewPeer(p2p.RoleOfferer, p2p.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer offerer.Close()
		sdp, err := offerer.CreateOffer(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		write(transfer.NewSDPOfferMessage(sdp))
		time.Sleep(200 * time.Millisecond)
	}

	write(transfer.NewP2PFailMessage("forced"))
	writeOneFileWS(t, write, content, filename)
	write(transfer.NewSessionEndMessage())

}

func runP2PFallback(t *testing.T, sendOffer bool) {
	relay := startTestRelay(t)
	defer relay.Close()
	relayURL := "ws" + relay.URL[4:]
	token := "p2pfail-test1234"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	destDir := t.TempDir()
	content := bytes.Repeat([]byte("fallback "), 20000)
	filename := "fallback.bin"

	var wg sync.WaitGroup
	var recvPath string
	var recvErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := transfer.NewReceiver(relayURL, token, key)
		if err := r.ConnectLive(); err != nil {
			recvErr = err
			return
		}
		defer r.Close()
		recvPath, recvErr = r.ReceiveFile(destDir, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	conn, _, err := websocket.DefaultDialer.Dial(relayURL+"/ws/"+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	fakeSenderP2PFail(t, conn, key, content, filename, sendOffer)
	conn.Close()

	wg.Wait()
	if recvErr != nil {
		t.Fatalf("receive: %v", recvErr)
	}
	got, err := os.ReadFile(recvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(got), len(content))
	}
	if filepath.Base(recvPath) != filename {
		t.Fatalf("filename: got %q want %q", filepath.Base(recvPath), filename)
	}
}

func TestStayOpen_WS_TwoBatches(t *testing.T) {
	relay := startTestRelay(t)
	defer relay.Close()
	relayURL := "ws" + relay.URL[4:]
	token := "stayopen-twobatch"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	destDir := t.TempDir()
	want1 := []byte("batch-one")
	want2 := []byte("batch-two-content")

	type batchSeen struct {
		idx   int
		paths []string
	}
	var seen []batchSeen

	var wg sync.WaitGroup
	var recvErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := transfer.NewReceiver(relayURL, token, key)
		if err := r.ConnectLive(); err != nil {
			recvErr = err
			return
		}
		defer r.Close()
		recvErr = r.ReceiveBatches(destDir, func(batchIdx int, paths []string) error {
			seen = append(seen, batchSeen{batchIdx, append([]string(nil), paths...)})
			return nil
		}, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	conn, _, err := websocket.DefaultDialer.Dial(relayURL+"/ws/"+token, nil)
	if err != nil {
		t.Fatal(err)
	}

	write := func(m transfer.Message) {
		ct, err := crypto.EncryptChunk(transfer.EncodeMessage(m), key)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, ct); err != nil {
			t.Fatal(err)
		}
	}
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	write(transfer.NewP2PFailMessage("forced"))
	writeOneFileWS(t, write, want1, "one.txt")
	write(transfer.NewSessionEndMessage())
	writeOneFileWS(t, write, want2, "two.bin")
	write(transfer.NewSessionEndMessage())
	conn.Close()

	wg.Wait()
	if recvErr != nil {
		t.Fatalf("receive: %v", recvErr)
	}
	if len(seen) != 2 {
		t.Fatalf("batches: got %d batches (%+v)", len(seen), seen)
	}
	if len(seen[0].paths) != 1 || len(seen[1].paths) != 1 {
		t.Fatalf("paths per batch: %#v", seen)
	}
	got1, err := os.ReadFile(seen[0].paths[0])
	if err != nil {
		t.Fatal(err)
	}
	got2, err := os.ReadFile(seen[1].paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got1, want1) || !bytes.Equal(got2, want2) {
		t.Fatalf("content mismatch: %q vs %q / %q vs %q", got1, want1, got2, want2)
	}
}

func TestReceiverFallback_P2PFailFirst(t *testing.T) {
	runP2PFallback(t, false)
}

func TestReceiverFallback_P2PFailAfterOffer(t *testing.T) {
	runP2PFallback(t, true)
}

func TestP2PFail_TwoFilesOverWS(t *testing.T) {
	relay := startTestRelay(t)
	defer relay.Close()
	relayURL := "ws" + relay.URL[4:]
	token := "p2pfail-multi12"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	destDir := t.TempDir()
	wantA := []byte("alpha-content")
	wantB := []byte("beta-second-file")

	var wg sync.WaitGroup
	var paths []string
	var recvErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := transfer.NewReceiver(relayURL, token, key)
		if err := r.ConnectLive(); err != nil {
			recvErr = err
			return
		}
		defer r.Close()
		paths, recvErr = r.ReceiveFiles(destDir, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	conn, _, err := websocket.DefaultDialer.Dial(relayURL+"/ws/"+token, nil)
	if err != nil {
		t.Fatal(err)
	}

	write := func(m transfer.Message) {
		ct, err := crypto.EncryptChunk(transfer.EncodeMessage(m), key)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, ct); err != nil {
			t.Fatal(err)
		}
	}
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	write(transfer.NewP2PFailMessage("forced"))
	writeOneFileWS(t, write, wantA, "a.txt")
	writeOneFileWS(t, write, wantB, "b.txt")
	write(transfer.NewSessionEndMessage())
	conn.Close()

	wg.Wait()
	if recvErr != nil {
		t.Fatalf("receive: %v", recvErr)
	}
	if len(paths) != 2 {
		t.Fatalf("paths: got %d want 2 (%v)", len(paths), paths)
	}

	gotA, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := os.ReadFile(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(gotA) != string(wantA) {
		t.Fatalf("file A: got %q want %q", gotA, wantA)
	}
	if string(gotB) != string(wantB) {
		t.Fatalf("file B: got %q want %q", gotB, wantB)
	}
}

// receiver hits its own NegotiateTimeout before any P2P_FAIL arrives (same-host race).
func TestReceiverFallback_OwnTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for NegotiateTimeout")
	}

	relay := startTestRelay(t)
	defer relay.Close()
	relayURL := "ws" + relay.URL[4:]
	token := "p2ptimeout-test1"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	destDir := t.TempDir()
	content := bytes.Repeat([]byte("timeout "), 8000)
	filename := "timeout.bin"

	var wg sync.WaitGroup
	var recvPath string
	var recvErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := transfer.NewReceiver(relayURL, token, key)
		if err := r.ConnectLive(); err != nil {
			recvErr = err
			return
		}
		defer r.Close()
		recvPath, recvErr = r.ReceiveFile(destDir, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	conn, _, err := websocket.DefaultDialer.Dial(relayURL+"/ws/"+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	write := func(m transfer.Message) {
		ct, err := crypto.EncryptChunk(transfer.EncodeMessage(m), key)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, ct); err != nil {
			t.Fatal(err)
		}
	}
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	offerer, err := p2p.NewPeer(p2p.RoleOfferer, p2p.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer offerer.Close()
	sdp, err := offerer.CreateOffer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	write(transfer.NewSDPOfferMessage(sdp))

	// wait past NegotiateTimeout without sending P2P_FAIL
	time.Sleep(transfer.NegotiateTimeout + time.Second)

	const chunk = 64 * 1024
	meta, err := transfer.NewMetadataMessage(filename, int64(len(content)), (len(content)+chunk-1)/chunk)
	if err != nil {
		t.Fatal(err)
	}
	write(meta)
	for off := 0; off < len(content); off += chunk {
		end := off + chunk
		if end > len(content) {
			end = len(content)
		}
		write(transfer.NewChunkMessage(content[off:end]))
	}
	write(transfer.NewCompleteMessage())
	write(transfer.NewSessionEndMessage())

	wg.Wait()
	if recvErr != nil {
		t.Fatalf("receive: %v", recvErr)
	}
	got, err := os.ReadFile(recvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(got), len(content))
	}
}

// senderIO returns encrypt-write and decrypt-read helpers for a fake sender socket.
func senderIO(t *testing.T, conn *websocket.Conn, key []byte) (func(transfer.Message), func() transfer.Message) {
	t.Helper()
	write := func(m transfer.Message) {
		ct, err := crypto.EncryptChunk(transfer.EncodeMessage(m), key)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, ct); err != nil {
			t.Fatal(err)
		}
	}
	read := func() transfer.Message {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("sender read: %v", err)
		}
		pt, err := crypto.DecryptChunk(data, key)
		if err != nil {
			t.Fatal(err)
		}
		m, err := transfer.DecodeMessage(pt)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	return write, read
}

// A reloaded browser sender restarts its handshake (Version, await Ready) on
// the same room. The receiver must re-handshake and accept the next batch.
func TestStayOpen_SenderReloadBetweenBatches(t *testing.T) {
	relay := startTestRelay(t)
	defer relay.Close()
	relayURL := "ws" + relay.URL[4:]
	token := "reload-betweenbat"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	want1 := []byte("before-reload")
	want2 := []byte("after-reload-content")

	var seen [][]string
	var wg sync.WaitGroup
	var recvErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := transfer.NewReceiver(relayURL, token, key)
		if err := r.ConnectLive(); err != nil {
			recvErr = err
			return
		}
		defer r.Close()
		recvErr = r.ReceiveBatches(destDir, func(_ int, paths []string) error {
			seen = append(seen, append([]string(nil), paths...))
			return nil
		}, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	conn, _, err := websocket.DefaultDialer.Dial(relayURL+"/ws/"+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	write, read := senderIO(t, conn, key)

	write(transfer.NewP2PFailMessage("forced"))
	writeOneFileWS(t, write, want1, "one.txt")
	write(transfer.NewSessionEndMessage())

	write(transfer.NewVersionMessage())
	if m := read(); m.Type != transfer.MsgTypeReady {
		t.Fatalf("expected Ready after version re-announce, got %#x", m.Type)
	}
	write(transfer.NewP2PFailMessage("forced"))
	writeOneFileWS(t, write, want2, "two.bin")
	write(transfer.NewSessionEndMessage())
	conn.Close()

	wg.Wait()
	if recvErr != nil {
		t.Fatalf("receive: %v", recvErr)
	}
	if len(seen) != 2 || len(seen[0]) != 1 || len(seen[1]) != 1 {
		t.Fatalf("batches: %#v", seen)
	}
	got1, _ := os.ReadFile(seen[0][0])
	got2, _ := os.ReadFile(seen[1][0])
	if !bytes.Equal(got1, want1) || !bytes.Equal(got2, want2) {
		t.Fatalf("content mismatch across reload")
	}
}

// A reload mid-file discards the partial file; the resent copy lands clean.
func TestSenderReloadMidFile_PartialDiscarded(t *testing.T) {
	relay := startTestRelay(t)
	defer relay.Close()
	relayURL := "ws" + relay.URL[4:]
	token := "reload-midfile12"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	want := bytes.Repeat([]byte("full-content "), 1000)

	var recvPaths []string
	var wg sync.WaitGroup
	var recvErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := transfer.NewReceiver(relayURL, token, key)
		if err := r.ConnectLive(); err != nil {
			recvErr = err
			return
		}
		defer r.Close()
		recvPaths, recvErr = r.ReceiveFiles(destDir, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	conn, _, err := websocket.DefaultDialer.Dial(relayURL+"/ws/"+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	write, read := senderIO(t, conn, key)

	write(transfer.NewP2PFailMessage("forced"))
	meta, err := transfer.NewMetadataMessage("doc.bin", int64(len(want)), 1)
	if err != nil {
		t.Fatal(err)
	}
	write(meta)
	write(transfer.NewChunkMessage(want[:100]))

	write(transfer.NewVersionMessage())
	if m := read(); m.Type != transfer.MsgTypeReady {
		t.Fatalf("expected Ready after mid-file restart, got %#x", m.Type)
	}
	write(transfer.NewP2PFailMessage("forced"))
	writeOneFileWS(t, write, want, "doc.bin")
	write(transfer.NewSessionEndMessage())
	conn.Close()

	wg.Wait()
	if recvErr != nil {
		t.Fatalf("receive: %v", recvErr)
	}
	if len(recvPaths) != 1 {
		t.Fatalf("paths: %#v", recvPaths)
	}
	got, err := os.ReadFile(recvPaths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch after mid-file restart")
	}
	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "doc.bin" {
		t.Fatalf("partial not discarded, dir: %v", entries)
	}
}

// A peer announcing a different protocol version mid-session must fail closed.
func TestVersionMismatchMidSession(t *testing.T) {
	relay := startTestRelay(t)
	defer relay.Close()
	relayURL := "ws" + relay.URL[4:]
	token := "reload-mismatch1"

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()

	var wg sync.WaitGroup
	var recvErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := transfer.NewReceiver(relayURL, token, key)
		if err := r.ConnectLive(); err != nil {
			recvErr = err
			return
		}
		defer r.Close()
		recvErr = r.ReceiveBatches(destDir, func(int, []string) error { return nil }, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	conn, _, err := websocket.DefaultDialer.Dial(relayURL+"/ws/"+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	write, _ := senderIO(t, conn, key)

	write(transfer.NewP2PFailMessage("forced"))
	writeOneFileWS(t, write, []byte("ok"), "ok.txt")
	write(transfer.NewSessionEndMessage())
	write(transfer.Message{Type: transfer.MsgTypeVersion, Payload: []byte{transfer.ProtocolVersion - 1}})

	wg.Wait()
	if recvErr == nil || !strings.Contains(recvErr.Error(), "protocol version mismatch") {
		t.Fatalf("expected version mismatch error, got %v", recvErr)
	}
}
