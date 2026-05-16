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
	"github.com/sanyamgarg/airpipe/internal/p2p"
)

const ChunkSize = 64 * 1024

type senderTransport int

const (
	senderTransportIdle senderTransport = iota
	senderTransportP2P
	senderTransportWS
)

type Sender struct {
	relayURL  string
	token     string
	key       []byte
	conn      *websocket.Conn
	peer      *p2p.Peer
	transport senderTransport
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

// open the room and sit quiet, receiver shows up later
func (s *Sender) ConnectLive() error {
	url := fmt.Sprintf("%s/ws/%s", s.relayURL, s.token)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %w", err)
	}
	s.conn = conn
	return nil
}

// block until the receiver pings us
func (s *Sender) WaitForPeer(timeout time.Duration) error {
	s.conn.SetReadDeadline(time.Now().Add(timeout))
	defer s.conn.SetReadDeadline(time.Time{})
	for {
		msg, err := readSignalMsg(s.conn, s.key)
		if err != nil {
			return fmt.Errorf("waiting for peer: %w", err)
		}
		if msg.Type == MsgTypePeerJoin {
			return nil
		}
	}
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

func (s *Sender) SendFile(filePath string, progressFn func(sent, total int64)) error {
	return s.SendFiles([]string{filePath}, func(_ int, sent, total int64) {
		if progressFn != nil {
			progressFn(sent, total)
		}
	})
}

// SendFiles sends one batch and tears down negotiated P2P (legacy one-shot callers).
func (s *Sender) SendFiles(paths []string, progressFn func(fileIndex int, sent, total int64)) error {
	if err := s.SendBatch(paths, progressFn); err != nil {
		return err
	}
	s.CloseTransport()
	return nil
}

func (s *Sender) wsSendWire() func([]byte) error {
	return func(data []byte) error {
		return s.conn.WriteMessage(websocket.BinaryMessage, data)
	}
}

func (s *Sender) negotiateOrReuse(ctx context.Context) (sendWire func([]byte) error, err error) {
	switch s.transport {
	case senderTransportP2P:
		if s.peer == nil || !s.peer.IsOpen() {
			return nil, fmt.Errorf("p2p transport closed")
		}
		return s.peer.Send, nil
	case senderTransportWS:
		return s.wsSendWire(), nil
	case senderTransportIdle:
		peer, nerr := negotiateSender(ctx, s.conn, s.key)
		if nerr == nil {
			fmt.Fprintln(os.Stderr, "transport: p2p")
			if sigErr := writeSignalMsg(s.conn, s.key, NewP2PReadyMessage()); sigErr != nil {
				peer.Close()
				s.peer = nil
				s.transport = senderTransportIdle
				return nil, fmt.Errorf("signal p2p ready: %w", sigErr)
			}
			s.peer = peer
			s.transport = senderTransportP2P
			return s.peer.Send, nil
		}
		fmt.Fprintf(os.Stderr, "transport: ws (p2p failed: %v)\n", nerr)
		_ = writeSignalMsg(s.conn, s.key, NewP2PFailMessage(nerr.Error()))
		s.transport = senderTransportWS
		return s.wsSendWire(), nil
	default:
		return nil, fmt.Errorf("sender: invalid transport state")
	}
}

// SendBatch streams each path over the negotiated transport for this WebSocket session, then sends SessionEnd without closing peer or WebSocket (use for persistent sessions).
func (s *Sender) SendBatch(paths []string, progressFn func(fileIndex int, sent, total int64)) error {
	if len(paths) == 0 {
		return fmt.Errorf("no files to send")
	}
	ctx := context.Background()

	sendWire, err := s.negotiateOrReuse(ctx)
	if err != nil {
		return err
	}

	for i, p := range paths {
		idx := i
		wrap := func(sent, total int64) {
			if progressFn != nil {
				progressFn(idx, sent, total)
			}
		}
		if streamErr := s.streamFile(sendWire, p, wrap); streamErr != nil {
			if s.transport == senderTransportP2P && s.peer != nil {
				s.peer.WaitDrain(10 * time.Second)
				s.peer.Close()
				s.peer = nil
				s.transport = senderTransportIdle
			}
			return streamErr
		}
	}

	if s.transport == senderTransportP2P && s.peer != nil {
		if sigErr := s.writeEncrypted(sendWire, NewSessionEndMessage()); sigErr != nil {
			s.peer.WaitDrain(10 * time.Second)
			s.peer.Close()
			s.peer = nil
			s.transport = senderTransportIdle
			return fmt.Errorf("send session end: %w", sigErr)
		}
		s.peer.WaitDrain(10 * time.Second)
		return nil
	}

	if err := s.writeEncrypted(sendWire, NewSessionEndMessage()); err != nil {
		return fmt.Errorf("send session end: %w", err)
	}
	return nil
}

// CloseTransport shuts down negotiated P2P for this Sender; WS relay mode stays on the socket until Close().
func (s *Sender) CloseTransport() {
	if s.peer != nil {
		s.peer.WaitDrain(10 * time.Second)
		s.peer.Close()
		s.peer = nil
	}
	if s.transport == senderTransportP2P {
		s.transport = senderTransportIdle
	}
}

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
	s.CloseTransport()
	s.transport = senderTransportIdle
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}
