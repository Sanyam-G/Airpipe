package transfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sanyamgarg/airpipe/internal/crypto"
)

const ChunkSize = 64 * 1024

// Sender drives the write side of a transfer over a token-scoped WS room. v2
// attempts WebRTC P2P first and falls back to streaming over the relay WS.
type Sender struct {
	relayURL string
	token    string
	key      []byte
	conn     *websocket.Conn
}

func NewSender(relayURL, token string, key []byte) *Sender {
	return &Sender{relayURL: relayURL, token: token, key: key}
}

func (s *Sender) Connect() error {
	url := fmt.Sprintf("%s/ws/%s", s.relayURL, s.token)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %w", err)
	}
	s.conn = conn

	encryptedVersion, err := crypto.EncryptChunk(EncodeMessage(NewVersionMessage()), s.key)
	if err != nil {
		return fmt.Errorf("failed to encrypt version message: %w", err)
	}
	if err := s.conn.WriteMessage(websocket.BinaryMessage, encryptedVersion); err != nil {
		return fmt.Errorf("failed to send version message: %w", err)
	}
	return nil
}

func (s *Sender) WaitForReceiver(timeout time.Duration) error {
	s.conn.SetReadDeadline(time.Now().Add(timeout))
	_, message, err := s.conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("timeout waiting for receiver: %w", err)
	}

	decrypted, err := crypto.DecryptChunk(message, s.key)
	if err != nil {
		return fmt.Errorf("failed to decrypt ready message: %w", err)
	}

	msg, err := DecodeMessage(decrypted)
	if err != nil {
		return err
	}
	if msg.Type != MsgTypeReady {
		return fmt.Errorf("unexpected message type: %d", msg.Type)
	}
	s.conn.SetReadDeadline(time.Time{})
	return nil
}

// SendFile attempts a WebRTC P2P transfer first, falling back to streaming
// through the relay WS when NAT punching fails or signaling times out. Bytes
// are NaCl-encrypted on top of DTLS in both paths; a malicious relay can only
// see ciphertext either way.
func (s *Sender) SendFile(filePath string, progressFn func(sent, total int64)) error {
	ctx := context.Background()

	peer, err := negotiateSender(ctx, s.conn, s.key)
	if err == nil {
		fmt.Fprintln(os.Stderr, "[airpipe] transport: P2P (WebRTC DataChannel)")
		if sigErr := writeSignalMsg(s.conn, s.key, NewP2PReadyMessage()); sigErr != nil {
			peer.Close()
			return fmt.Errorf("signal p2p ready: %w", sigErr)
		}
		streamErr := s.streamFile(peer.Send, filePath, progressFn)
		// Drain the DataChannel before tearing down so the Complete message
		// and any still-buffered chunks actually reach the receiver.
		peer.WaitDrain(10 * time.Second)
		peer.Close()
		return streamErr
	}

	fmt.Fprintf(os.Stderr, "[airpipe] transport: WS relay fallback (P2P negotiation failed: %v)\n", err)
	_ = writeSignalMsg(s.conn, s.key, NewP2PFailMessage(err.Error()))
	wsSend := func(data []byte) error {
		return s.conn.WriteMessage(websocket.BinaryMessage, data)
	}
	return s.streamFile(wsSend, filePath, progressFn)
}

// streamFile is shared between the P2P and WS paths. `sendWire` receives
// already-serialized wire bytes and delivers them to whichever transport.
func (s *Sender) streamFile(sendWire func([]byte) error, filePath string, progressFn func(sent, total int64)) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	filename := filepath.Base(filePath)
	fileSize := stat.Size()
	totalChunks := int((fileSize + ChunkSize - 1) / ChunkSize)

	metaMsg, err := NewMetadataMessage(filename, fileSize, totalChunks)
	if err != nil {
		return err
	}
	if err := s.writeEncrypted(sendWire, metaMsg); err != nil {
		return fmt.Errorf("send metadata: %w", err)
	}

	buf := make([]byte, ChunkSize)
	var bytesSent int64
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			if err := s.writeEncrypted(sendWire, NewChunkMessage(buf[:n])); err != nil {
				return fmt.Errorf("send chunk: %w", err)
			}
			bytesSent += int64(n)
			if progressFn != nil {
				progressFn(bytesSent, fileSize)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read file: %w", readErr)
		}
	}

	if err := s.writeEncrypted(sendWire, NewCompleteMessage()); err != nil {
		return fmt.Errorf("send complete: %w", err)
	}
	return nil
}

func (s *Sender) writeEncrypted(sendWire func([]byte) error, msg Message) error {
	enc, err := crypto.EncryptChunk(EncodeMessage(msg), s.key)
	if err != nil {
		return err
	}
	return sendWire(enc)
}

func (s *Sender) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}
