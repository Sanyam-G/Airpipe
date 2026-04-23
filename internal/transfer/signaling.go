package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/p2p"
)

// NegotiateTimeout bounds how long either side waits for WebRTC negotiation
// before giving up and falling back to WS streaming.
const NegotiateTimeout = 15 * time.Second

func writeSignalMsg(conn *websocket.Conn, key []byte, msg Message) error {
	encrypted, err := crypto.EncryptChunk(EncodeMessage(msg), key)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, encrypted)
}

// readSignalMsg decrypts and decodes the next message. It returns a net.Error
// with Timeout()==true when a read deadline fires, which lets the caller poll
// while also waiting on another event.
func readSignalMsg(conn *websocket.Conn, key []byte) (Message, error) {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return Message{}, err
	}
	decrypted, err := crypto.DecryptChunk(raw, key)
	if err != nil {
		return Message{}, fmt.Errorf("decrypt signal: %w", err)
	}
	return DecodeMessage(decrypted)
}

// readerLoop runs in a goroutine doing blocking reads from the WS conn until
// the conn closes or the loop is told to stop via stopCh. Each message goes
// onto out. Errors close errCh. gorilla/websocket panics if you try to read
// after a previous read error, so we keep all reads in this single loop and
// signal exit via close(out) / errCh.
type wsRead struct {
	msg Message
	err error
}

func startWSReader(conn *websocket.Conn, key []byte, stopCh <-chan struct{}) <-chan wsRead {
	out := make(chan wsRead, 16)
	go func() {
		defer close(out)
		for {
			msg, err := readSignalMsg(conn, key)
			select {
			case out <- wsRead{msg: msg, err: err}:
			case <-stopCh:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return out
}

// negotiateSender drives the WebRTC SDP/ICE handshake from the offerer side.
// Returns a peer whose DataChannel is open, or an error.
func negotiateSender(ctx context.Context, conn *websocket.Conn, key []byte) (*p2p.Peer, error) {
	negCtx, cancel := context.WithTimeout(ctx, NegotiateTimeout)
	defer cancel()

	peer, err := p2p.NewPeer(p2p.RoleOfferer, p2p.Config{})
	if err != nil {
		return nil, err
	}

	offer, err := peer.CreateOffer(negCtx)
	if err != nil {
		peer.Close()
		return nil, err
	}
	if err := writeSignalMsg(conn, key, NewSDPOfferMessage(offer)); err != nil {
		peer.Close()
		return nil, err
	}

	stopCh := make(chan struct{})
	defer close(stopCh)
	reads := startWSReader(conn, key, stopCh)

	// Trickle local candidates in the background.
	trickleDone := make(chan struct{})
	go func() {
		defer close(trickleDone)
		for {
			select {
			case c, ok := <-peer.LocalICECandidates():
				if !ok {
					return
				}
				raw, _ := json.Marshal(c)
				_ = writeSignalMsg(conn, key, NewICECandidateMessage(raw))
			case <-negCtx.Done():
				return
			case <-peer.Closed():
				return
			}
		}
	}()

	openCh := make(chan struct{})
	go func() {
		_ = peer.WaitOpen(negCtx)
		close(openCh)
	}()

	for {
		select {
		case <-openCh:
			if !peer.IsOpen() {
				peer.Close()
				return nil, fmt.Errorf("datachannel did not open within %s", NegotiateTimeout)
			}
			cancel()
			<-trickleDone
			return peer, nil
		case r, ok := <-reads:
			if !ok {
				peer.Close()
				return nil, fmt.Errorf("signaling channel closed")
			}
			if r.err != nil {
				peer.Close()
				return nil, fmt.Errorf("read signal: %w", r.err)
			}
			switch r.msg.Type {
			case MsgTypeSDPAnswer:
				if err := peer.SetRemoteAnswer(negCtx, string(r.msg.Payload)); err != nil {
					peer.Close()
					return nil, fmt.Errorf("set remote answer: %w", err)
				}
			case MsgTypeICECandidate:
				_ = peer.AddICECandidate(r.msg.Payload)
			case MsgTypeP2PFail:
				peer.Close()
				return nil, fmt.Errorf("peer reported p2p failure: %s", string(r.msg.Payload))
			}
		case <-negCtx.Done():
			peer.Close()
			return nil, negCtx.Err()
		}
	}
}

// negotiateReceiver handles the answerer side. The caller has already consumed
// the initial SDPOffer message and passes its payload in.
func negotiateReceiver(ctx context.Context, conn *websocket.Conn, key []byte, offerSDP string) (*p2p.Peer, error) {
	negCtx, cancel := context.WithTimeout(ctx, NegotiateTimeout)
	defer cancel()

	peer, err := p2p.NewPeer(p2p.RoleAnswerer, p2p.Config{})
	if err != nil {
		return nil, err
	}

	answer, err := peer.SetRemoteOffer(negCtx, offerSDP)
	if err != nil {
		peer.Close()
		return nil, err
	}
	if err := writeSignalMsg(conn, key, NewSDPAnswerMessage(answer)); err != nil {
		peer.Close()
		return nil, err
	}

	stopCh := make(chan struct{})
	defer close(stopCh)
	reads := startWSReader(conn, key, stopCh)

	trickleDone := make(chan struct{})
	go func() {
		defer close(trickleDone)
		for {
			select {
			case c, ok := <-peer.LocalICECandidates():
				if !ok {
					return
				}
				raw, _ := json.Marshal(c)
				_ = writeSignalMsg(conn, key, NewICECandidateMessage(raw))
			case <-negCtx.Done():
				return
			case <-peer.Closed():
				return
			}
		}
	}()

	openCh := make(chan struct{})
	go func() {
		_ = peer.WaitOpen(negCtx)
		close(openCh)
	}()

	// Receiver holds the only WS reader. Don't return until BOTH the DC is
	// open AND the sender has sent P2PReady — otherwise the caller would
	// race a second concurrent reader on the same conn (gorilla panics).
	dcOpen, p2pReady := false, false
	for {
		if dcOpen && p2pReady {
			cancel()
			<-trickleDone
			return peer, nil
		}
		select {
		case <-openCh:
			dcOpen = true
		case r, ok := <-reads:
			if !ok {
				peer.Close()
				return nil, fmt.Errorf("signaling channel closed")
			}
			if r.err != nil {
				peer.Close()
				return nil, fmt.Errorf("read signal: %w", r.err)
			}
			switch r.msg.Type {
			case MsgTypeICECandidate:
				_ = peer.AddICECandidate(r.msg.Payload)
			case MsgTypeP2PReady:
				p2pReady = true
			case MsgTypeP2PFail:
				peer.Close()
				return nil, fmt.Errorf("peer reported p2p failure: %s", string(r.msg.Payload))
			}
		case <-negCtx.Done():
			peer.Close()
			return nil, negCtx.Err()
		}
	}
}
