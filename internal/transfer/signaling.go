package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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

func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}

// negotiateSender drives the WebRTC SDP/ICE handshake from the offerer side
// over an already-connected, version-negotiated WS conn. Returns a peer whose
// DataChannel is open, or an error if negotiation fails / times out. The WS
// conn is left ready for further use by the caller.
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

	// Poll-read remote messages while also watching for DataChannel open.
	for {
		if peer.IsOpen() {
			_ = conn.SetReadDeadline(time.Time{})
			cancel()
			<-trickleDone
			return peer, nil
		}
		if err := negCtx.Err(); err != nil {
			peer.Close()
			return nil, err
		}

		_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		msg, err := readSignalMsg(conn, key)
		if err != nil {
			if isTimeout(err) {
				continue
			}
			peer.Close()
			return nil, err
		}

		switch msg.Type {
		case MsgTypeSDPAnswer:
			if err := peer.SetRemoteAnswer(negCtx, string(msg.Payload)); err != nil {
				peer.Close()
				return nil, fmt.Errorf("set remote answer: %w", err)
			}
		case MsgTypeICECandidate:
			_ = peer.AddICECandidate(msg.Payload)
		case MsgTypeP2PFail:
			peer.Close()
			return nil, fmt.Errorf("peer reported p2p failure: %s", string(msg.Payload))
		}
	}
}

// negotiateReceiver handles the answerer side. It expects the caller to have
// already consumed the initial SDPOffer message and passes its payload in.
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

	for {
		if peer.IsOpen() {
			_ = conn.SetReadDeadline(time.Time{})
			cancel()
			<-trickleDone
			return peer, nil
		}
		if err := negCtx.Err(); err != nil {
			peer.Close()
			return nil, err
		}

		_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		msg, err := readSignalMsg(conn, key)
		if err != nil {
			if isTimeout(err) {
				continue
			}
			peer.Close()
			return nil, err
		}

		switch msg.Type {
		case MsgTypeICECandidate:
			_ = peer.AddICECandidate(msg.Payload)
		case MsgTypeP2PFail:
			peer.Close()
			return nil, fmt.Errorf("peer reported p2p failure: %s", string(msg.Payload))
		}
	}
}
