package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/p2p"
)

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
	// filepath.Clean rewrites inputs like ". " or " .."
	if filepath.Clean(raw) != raw {
		return "", fmt.Errorf("invalid filename: %q", raw)
	}
	return raw, nil
}

type msgReader func() (Message, error)

// errSenderRestarted: the sender re-announced its version mid-session (a
// reloaded browser page restarting its handshake); the receive session must
// be re-opened from transport negotiation.
var errSenderRestarted = errors.New("sender restarted")

type Receiver struct {
	relayURL string
	token    string
	key      []byte
	conn     *websocket.Conn
}

func NewReceiver(relayURL, token string, key []byte) *Receiver {
	return &Receiver{relayURL: relayURL, token: token, key: key}
}

// open the room and ping that we're here
func (r *Receiver) ConnectLive() error {
	url := fmt.Sprintf("%s/ws/%s", r.relayURL, r.token)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %w", err)
	}
	r.conn = conn
	if err := writeSignalMsg(r.conn, r.key, NewPeerJoinMessage()); err != nil {
		return fmt.Errorf("failed to announce peer-join: %w", err)
	}
	return nil
}

func (r *Receiver) Connect() error {
	url := fmt.Sprintf("%s/ws/%s", r.relayURL, r.token)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %w", err)
	}
	r.conn = conn

	r.conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
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

func (r *Receiver) ReceiveFile(destDir string, progressFn func(received, total int64)) (string, error) {
	paths, err := r.ReceiveFiles(destDir, func(_ int, received, total int64) {
		if progressFn != nil {
			progressFn(received, total)
		}
	})
	if err != nil {
		return "", err
	}
	if len(paths) != 1 {
		return "", fmt.Errorf("expected a single file, got %d", len(paths))
	}
	return paths[0], nil
}

// ReceiveFiles reads one transport negotiation, then one batch until MsgTypeSessionEnd (or clean disconnect), then tears down the negotiated transport.
// receiveSessionCleanup runs after all files have been drained; negotiationStop must run before PeerClose when both are set (tear down the WS signalling reader before closing the RTC peer).
type receiveSessionCleanup struct {
	NegotiationStop func()
	PeerClose       func()
}

func (c receiveSessionCleanup) run() {
	if c.NegotiationStop != nil {
		c.NegotiationStop()
	}
	if c.PeerClose != nil {
		c.PeerClose()
	}
}

// receiveBatch reads files until MsgTypeSessionEnd or until the sender disconnects cleanly.
// If sessionStillOpen is true, the sender sent SessionEnd and may send another batch on the same transport.
// When allowIdleBetweenBatches is true, a clean read error or SessionEnd before the first Metadata of this batch ends the batch with zero paths (used after the opening batch in ReceiveBatches).
func (r *Receiver) receiveBatch(read msgReader, destDir string, progressFn func(fileIndex int, received, total int64), primed *Message, allowIdleBetweenBatches bool) (paths []string, sessionStillOpen bool, err error) {
	nextPrimed := primed

	if nextPrimed == nil && allowIdleBetweenBatches {
		first, err := read()
		if isSessionEndReadErr(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("read message: %w", err)
		}
		switch first.Type {
		case MsgTypeMetadata:
			nextPrimed = &first
		case MsgTypeSessionEnd:
			return nil, false, nil
		case MsgTypeVersion:
			if verr := r.reHandshake(first); verr != nil {
				return nil, false, verr
			}
			return nil, false, errSenderRestarted
		default:
			return nil, false, fmt.Errorf("unexpected message while waiting for next batch (%#x)", first.Type)
		}
	}

	var fileIdx int
	for {
		wrapProgress := func(received, total int64) {
			if progressFn != nil {
				progressFn(fileIdx, received, total)
			}
		}
		path, rerr := r.recvFile(read, destDir, wrapProgress, nextPrimed)
		nextPrimed = nil
		if rerr != nil {
			return paths, false, rerr
		}
		paths = append(paths, path)
		fileIdx++

		msg, peekErr := read()
		if peekErr != nil {
			if len(paths) > 0 && isSessionEndReadErr(peekErr) {
				return paths, false, nil
			}
			if len(paths) == 0 && isSessionEndReadErr(peekErr) {
				return nil, false, fmt.Errorf("connection closed before any file was received")
			}
			return paths, false, fmt.Errorf("read message: %w", peekErr)
		}
		switch msg.Type {
		case MsgTypeSessionEnd:
			return paths, true, nil
		case MsgTypeMetadata:
			nextPrimed = &msg
		case MsgTypeVersion:
			if verr := r.reHandshake(msg); verr != nil {
				return paths, false, verr
			}
			return paths, false, errSenderRestarted
		default:
			return paths, false, fmt.Errorf("unexpected message after file (%#x)", msg.Type)
		}
	}
}

func (r *Receiver) ReceiveFiles(destDir string, progressFn func(fileIndex int, received, total int64)) ([]string, error) {
	info, err := os.Stat(destDir)
	if err != nil {
		return nil, fmt.Errorf("destination directory %q does not exist: %w", destDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("destination path %q is not a directory", destDir)
	}

	read, sessCleanup, primed, err := r.openReceiveSession()
	defer func() { sessCleanup.run() }()
	if err != nil {
		return nil, err
	}

	for {
		paths, _, rerr := r.receiveBatch(read, destDir, progressFn, primed, false)
		if !errors.Is(rerr, errSenderRestarted) {
			return paths, rerr
		}
		sessCleanup.run()
		read, sessCleanup, primed, err = r.openReceiveSession()
		if err != nil {
			return nil, err
		}
	}
}

// ReceiveBatches negotiates once, then receives zero or more batches until the sender closes the connection.
// The progressFn fileIndex restarts at 0 for each batch. onBatch is invoked after each MsgTypeSessionEnd (or after a clean disconnect that ended a batch).
func (r *Receiver) ReceiveBatches(destDir string, onBatch func(batchIndex int, paths []string) error, progressFn func(fileIndex int, received, total int64)) error {
	info, err := os.Stat(destDir)
	if err != nil {
		return fmt.Errorf("destination directory %q does not exist: %w", destDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("destination path %q is not a directory", destDir)
	}

	read, sessCleanup, primed, err := r.openReceiveSession()
	defer func() { sessCleanup.run() }()
	if err != nil {
		return err
	}

	batchIdx := 0
	nextPrimed := primed
	for {
		paths, sessionOpen, rerr := r.receiveBatch(read, destDir, progressFn, nextPrimed, batchIdx > 0)
		nextPrimed = nil
		if errors.Is(rerr, errSenderRestarted) {
			if len(paths) > 0 {
				if err := onBatch(batchIdx, paths); err != nil {
					return err
				}
				batchIdx++
			}
			sessCleanup.run()
			read, sessCleanup, nextPrimed, err = r.openReceiveSession()
			if err != nil {
				return err
			}
			continue
		}
		if rerr != nil {
			return rerr
		}
		if len(paths) == 0 {
			return nil
		}
		if err := onBatch(batchIdx, paths); err != nil {
			return err
		}
		batchIdx++
		if !sessionOpen {
			return nil
		}
	}
}

// reHandshake answers a mid-session version announce. A reloaded web sender
// rejoins the room and restarts its handshake on the receiver's open socket,
// so validate the version and re-send Ready to unblock it (#13 reload edge).
func (r *Receiver) reHandshake(msg Message) error {
	if len(msg.Payload) == 0 || msg.Payload[0] != ProtocolVersion {
		got := byte(0)
		if len(msg.Payload) > 0 {
			got = msg.Payload[0]
		}
		return fmt.Errorf("protocol version mismatch: got %d, expected %d (run `airpipe update`)", got, ProtocolVersion)
	}
	return writeSignalMsg(r.conn, r.key, NewReadyMessage())
}

func isSessionEndReadErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
		return true
	}
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived, websocket.CloseAbnormalClosure) {
		return true
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	return false
}

func (r *Receiver) openReceiveSession() (read msgReader, cleanup receiveSessionCleanup, primed *Message, err error) {
	r.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	first, err := readSignalMsg(r.conn, r.key)
	for err == nil && first.Type == MsgTypeVersion {
		if verr := r.reHandshake(first); verr != nil {
			return nil, cleanup, nil, verr
		}
		first, err = readSignalMsg(r.conn, r.key)
	}
	if err != nil {
		return nil, cleanup, nil, fmt.Errorf("read first message: %w", err)
	}
	r.conn.SetReadDeadline(time.Time{})

	switch first.Type {
	case MsgTypeSDPOffer:
		peer, tail, stop, nerr := negotiateReceiver(context.Background(), r.conn, r.key, string(first.Payload))
		if nerr != nil {
			if errors.Is(nerr, ErrPeerP2PFail) {
				cleanup.NegotiationStop = stop
				return tail, cleanup, nil, nil
			}
			return nil, cleanup, nil, fmt.Errorf("p2p negotiation: %w", nerr)
		}
		cleanup.PeerClose = func() { peer.Close() }
		return r.peerReader(peer), cleanup, nil, nil

	case MsgTypeP2PFail:
		return r.wsReader(), cleanup, nil, nil

	case MsgTypeMetadata, MsgTypeChunk, MsgTypeComplete, MsgTypeError:
		return r.wsReader(), cleanup, &first, nil

	default:
		return nil, cleanup, nil, fmt.Errorf("unexpected first message type: %#x", first.Type)
	}
}

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
		case MsgTypeSessionEnd:
			return "", false, fmt.Errorf("session end before file finished")
		case MsgTypeVersion:
			if file != nil {
				file.Close()
				file = nil
				os.Remove(destPath)
				fmt.Fprintf(os.Stderr, "sender restarted mid-transfer, partial file discarded: %s\n", destPath)
			}
			if err := r.reHandshake(msg); err != nil {
				return "", false, err
			}
			return "", false, errSenderRestarted
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
