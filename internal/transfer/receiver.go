package transfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/p2p"
)

// SafeFilename validates a sender-provided filename. It rejects anything with
// path components, traversal sequences, or control characters rather than
// silently sanitizing — loud rejection surfaces malicious senders instead of
// hiding them.
func SafeFilename(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty filename")
	}
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("filename contains null byte")
	}
	if strings.ContainsAny(raw, `/\`) {
		return "", fmt.Errorf("filename contains path separator: %q", raw)
	}
	if raw == "." || raw == ".." {
		return "", fmt.Errorf("invalid filename: %q", raw)
	}
	// Catch sneaky inputs like ". " or " .." that filepath.Clean rewrites.
	if filepath.Clean(raw) != raw {
		return "", fmt.Errorf("invalid filename: %q", raw)
	}
	return raw, nil
}

type Receiver struct {
	relayURL string
	token    string
	key      []byte
	conn     *websocket.Conn
}

func NewReceiver(relayURL, token string, key []byte) *Receiver {
	return &Receiver{relayURL: relayURL, token: token, key: key}
}

func (r *Receiver) Connect() error {
	url := fmt.Sprintf("%s/ws/%s", r.relayURL, r.token)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %w", err)
	}
	r.conn = conn

	r.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, versionData, err := r.conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("failed to read version message: %w", err)
	}
	decryptedVersion, err := crypto.DecryptChunk(versionData, r.key)
	if err != nil {
		return fmt.Errorf("failed to decrypt version message: %w", err)
	}
	versionMsg, err := DecodeMessage(decryptedVersion)
	if err != nil {
		return fmt.Errorf("failed to decode version message: %w", err)
	}
	if versionMsg.Type != MsgTypeVersion || len(versionMsg.Payload) == 0 || versionMsg.Payload[0] != ProtocolVersion {
		got := byte(0)
		if len(versionMsg.Payload) > 0 {
			got = versionMsg.Payload[0]
		}
		return fmt.Errorf("protocol version mismatch: got %d, expected %d (run `airpipe update`)", got, ProtocolVersion)
	}
	r.conn.SetReadDeadline(time.Time{})

	readyMsg := NewReadyMessage()
	encryptedReady, err := crypto.EncryptChunk(EncodeMessage(readyMsg), r.key)
	if err != nil {
		return fmt.Errorf("failed to encrypt ready message: %w", err)
	}
	if err := r.conn.WriteMessage(websocket.BinaryMessage, encryptedReady); err != nil {
		return fmt.Errorf("failed to send ready message: %w", err)
	}
	return nil
}

func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := path[:len(path)-len(ext)]
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s(%d)%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// ReceiveFile consumes a transfer. The sender picks the transport by sending
// SDPOffer (P2P attempt), P2PFail (sender already decided WS), or Metadata
// directly (WS streaming first chunk). Path-traversal defense lives in
// recvFile via SafeFilename.
func (r *Receiver) ReceiveFile(destDir string, progressFn func(received, total int64)) (string, error) {
	info, err := os.Stat(destDir)
	if err != nil {
		return "", fmt.Errorf("destination directory %q does not exist: %w", destDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("destination path %q is not a directory", destDir)
	}

	r.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	first, err := readSignalMsg(r.conn, r.key)
	if err != nil {
		return "", fmt.Errorf("read first message: %w", err)
	}
	r.conn.SetReadDeadline(time.Time{})

	switch first.Type {
	case MsgTypeSDPOffer:
		// negotiateReceiver waits for both the DC to open AND P2PReady from
		// the sender before returning. After it returns, the WS isn't read
		// again on this side — chunks flow over the DC.
		peer, err := negotiateReceiver(context.Background(), r.conn, r.key, string(first.Payload))
		if err != nil {
			return "", fmt.Errorf("p2p negotiation: %w", err)
		}
		defer peer.Close()
		return r.recvFile(r.peerReader(peer), destDir, progressFn, nil)

	case MsgTypeP2PFail:
		return r.recvFile(r.wsReader(), destDir, progressFn, nil)

	case MsgTypeMetadata, MsgTypeChunk, MsgTypeComplete, MsgTypeError:
		return r.recvFile(r.wsReader(), destDir, progressFn, &first)

	default:
		return "", fmt.Errorf("unexpected first message type: %#x", first.Type)
	}
}

type msgReader func() (Message, error)

func (r *Receiver) wsReader() msgReader {
	return func() (Message, error) {
		r.conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		_, encrypted, err := r.conn.ReadMessage()
		if err != nil {
			return Message{}, err
		}
		plaintext, err := crypto.DecryptChunk(encrypted, r.key)
		if err != nil {
			return Message{}, err
		}
		return DecodeMessage(plaintext)
	}
}

func (r *Receiver) peerReader(peer *p2p.Peer) msgReader {
	return func() (Message, error) {
		select {
		case data, ok := <-peer.Messages():
			if !ok {
				return Message{}, io.EOF
			}
			plaintext, err := crypto.DecryptChunk(data, r.key)
			if err != nil {
				return Message{}, err
			}
			return DecodeMessage(plaintext)
		case <-time.After(5 * time.Minute):
			return Message{}, fmt.Errorf("p2p read timeout")
		}
	}
}

func (r *Receiver) recvFile(read msgReader, destDir string, progressFn func(received, total int64), primed *Message) (string, error) {
	var metadata Metadata
	var file *os.File
	var bytesReceived int64
	var destPath string

	defer func() {
		if file != nil {
			file.Close()
		}
	}()

	handle := func(msg Message) (string, bool, error) {
		switch msg.Type {
		case MsgTypeMetadata:
			meta, err := ParseMetadata(msg.Payload)
			if err != nil {
				return "", false, fmt.Errorf("parse metadata: %w", err)
			}
			safeName, err := SafeFilename(meta.Filename)
			if err != nil {
				return "", false, fmt.Errorf("unsafe filename from sender: %w", err)
			}
			metadata = meta
			destPath = uniquePath(filepath.Join(destDir, safeName))
			f, err := os.Create(destPath)
			if err != nil {
				return "", false, fmt.Errorf("create file: %w", err)
			}
			file = f
		case MsgTypeChunk:
			if file == nil {
				return "", false, fmt.Errorf("received chunk before metadata")
			}
			n, err := file.Write(msg.Payload)
			if err != nil {
				return "", false, fmt.Errorf("write chunk: %w", err)
			}
			bytesReceived += int64(n)
			if progressFn != nil {
				progressFn(bytesReceived, metadata.Size)
			}
		case MsgTypeComplete:
			return destPath, true, nil
		case MsgTypeError:
			return "", false, fmt.Errorf("sender error: %s", string(msg.Payload))
		}
		return "", false, nil
	}

	if primed != nil {
		if path, done, err := handle(*primed); err != nil {
			return "", err
		} else if done {
			return path, nil
		}
	}

	for {
		msg, err := read()
		if err != nil {
			return "", fmt.Errorf("read message: %w", err)
		}
		path, done, err := handle(msg)
		if err != nil {
			return "", err
		}
		if done {
			return path, nil
		}
	}
}

func (r *Receiver) Close() error {
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}
