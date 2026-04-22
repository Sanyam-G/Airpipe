// Package p2p wraps pion/webrtc into a narrow API that the rest of airpipe
// uses to move bytes over a DataChannel. Signaling (SDP + ICE exchange) is
// the caller's job: this package exposes channels for outgoing candidates
// and accepts incoming ones via AddICECandidate.
package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
)

// DefaultICEServers is a STUN-only list. TURN is deliberately absent because
// the relay already serves as the fallback transport when NAT traversal fails.
var DefaultICEServers = []webrtc.ICEServer{
	{URLs: []string{"stun:stun.l.google.com:19302"}},
	{URLs: []string{"stun:stun1.l.google.com:19302"}},
	{URLs: []string{"stun:stun.cloudflare.com:3478"}},
}

// Role identifies the WebRTC handshake side.
type Role int

const (
	RoleOfferer  Role = iota // creates the DataChannel and the SDP offer
	RoleAnswerer             // answers the offer; receives the DataChannel via OnDataChannel
)

// Config tunes Peer behaviour. Zero values are safe defaults.
type Config struct {
	ICEServers           []webrtc.ICEServer
	BackpressureHighMark uint64 // bufferedAmount threshold (bytes) that pauses sends
}

// Peer is a WebRTC peer with a single reliable, ordered DataChannel.
type Peer struct {
	pc   *webrtc.PeerConnection
	dc   *webrtc.DataChannel
	role Role
	cfg  Config

	localCandidates chan webrtc.ICECandidateInit
	dataChannelOpen chan struct{}
	incoming        chan []byte

	closed     chan struct{}
	closeOnce  sync.Once
	incomingMu sync.Mutex
	incClosed  bool

	bytesSent     atomic.Int64
	bytesReceived atomic.Int64
}

// NewPeer builds a Peer. Offerers get a DataChannel straight away; answerers
// get one asynchronously when SetRemoteOffer is called with a matching offer.
func NewPeer(role Role, cfg Config) (*Peer, error) {
	if cfg.ICEServers == nil {
		cfg.ICEServers = DefaultICEServers
	}
	if cfg.BackpressureHighMark == 0 {
		cfg.BackpressureHighMark = 8 << 20 // 8 MB
	}

	api := webrtc.NewAPI()
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: cfg.ICEServers})
	if err != nil {
		return nil, fmt.Errorf("new peer connection: %w", err)
	}

	p := &Peer{
		pc:              pc,
		role:            role,
		cfg:             cfg,
		localCandidates: make(chan webrtc.ICECandidateInit, 32),
		dataChannelOpen: make(chan struct{}),
		incoming:        make(chan []byte, 32),
		closed:          make(chan struct{}),
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		select {
		case p.localCandidates <- c.ToJSON():
		case <-p.closed:
		}
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateDisconnected {
			p.Close()
		}
	})

	if role == RoleOfferer {
		ordered := true
		dc, dcErr := pc.CreateDataChannel("airpipe", &webrtc.DataChannelInit{Ordered: &ordered})
		if dcErr != nil {
			pc.Close()
			return nil, fmt.Errorf("create data channel: %w", dcErr)
		}
		p.attachDataChannel(dc)
	} else {
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			p.attachDataChannel(dc)
		})
	}

	return p, nil
}

func (p *Peer) attachDataChannel(dc *webrtc.DataChannel) {
	p.dc = dc
	var openOnce sync.Once
	dc.OnOpen(func() {
		openOnce.Do(func() { close(p.dataChannelOpen) })
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// Pion reuses the buffer between callbacks; copy before publishing.
		data := make([]byte, len(msg.Data))
		copy(data, msg.Data)
		p.bytesReceived.Add(int64(len(data)))
		p.deliver(data)
	})
	dc.OnClose(func() {
		p.closeIncoming()
	})
}

func (p *Peer) deliver(data []byte) {
	p.incomingMu.Lock()
	if p.incClosed {
		p.incomingMu.Unlock()
		return
	}
	p.incomingMu.Unlock()
	select {
	case p.incoming <- data:
	case <-p.closed:
	}
}

func (p *Peer) closeIncoming() {
	p.incomingMu.Lock()
	defer p.incomingMu.Unlock()
	if p.incClosed {
		return
	}
	p.incClosed = true
	close(p.incoming)
}

// CreateOffer produces the local SDP offer for an offerer. The caller ships
// it across the signaling channel.
func (p *Peer) CreateOffer(ctx context.Context) (string, error) {
	if p.role != RoleOfferer {
		return "", errors.New("only offerer can create offer")
	}
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return "", err
	}
	return offer.SDP, nil
}

// SetRemoteOffer applies a remote SDP offer on an answerer and returns the
// answer SDP to ship back.
func (p *Peer) SetRemoteOffer(ctx context.Context, sdp string) (string, error) {
	if p.role != RoleAnswerer {
		return "", errors.New("only answerer can apply a remote offer")
	}
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdp,
	}); err != nil {
		return "", err
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", err
	}
	return answer.SDP, nil
}

// SetRemoteAnswer applies the SDP answer on the offerer.
func (p *Peer) SetRemoteAnswer(ctx context.Context, sdp string) error {
	return p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	})
}

// AddICECandidate feeds a remote ICE candidate received from signaling.
func (p *Peer) AddICECandidate(raw []byte) error {
	var c webrtc.ICECandidateInit
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("decode ICE candidate: %w", err)
	}
	return p.pc.AddICECandidate(c)
}

// LocalICECandidates returns the channel of candidates gathered on this peer
// that the caller should forward over signaling.
func (p *Peer) LocalICECandidates() <-chan webrtc.ICECandidateInit {
	return p.localCandidates
}

// WaitOpen blocks until the DataChannel is open, the context is cancelled,
// or the peer closes.
func (p *Peer) WaitOpen(ctx context.Context) error {
	select {
	case <-p.dataChannelOpen:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.closed:
		return errors.New("peer closed before datachannel opened")
	}
}

// Send writes a single message over the DataChannel. Applies simple
// bufferedAmount-based backpressure.
func (p *Peer) Send(data []byte) error {
	if p.dc == nil {
		return errors.New("datachannel not ready")
	}
	for p.dc.BufferedAmount() > p.cfg.BackpressureHighMark {
		select {
		case <-time.After(5 * time.Millisecond):
		case <-p.closed:
			return errors.New("peer closed")
		}
	}
	if err := p.dc.Send(data); err != nil {
		return err
	}
	p.bytesSent.Add(int64(len(data)))
	return nil
}

// Messages yields incoming DataChannel payloads. Closed when the peer or
// channel closes.
func (p *Peer) Messages() <-chan []byte { return p.incoming }

// Closed returns a channel closed when the peer is torn down.
func (p *Peer) Closed() <-chan struct{} { return p.closed }

// IsOpen returns true once the DataChannel is open and ready for Send.
func (p *Peer) IsOpen() bool {
	select {
	case <-p.dataChannelOpen:
		return true
	default:
		return false
	}
}

// Close tears down the peer connection and releases resources.
func (p *Peer) Close() error {
	p.closeOnce.Do(func() {
		close(p.closed)
		if p.dc != nil {
			p.dc.Close()
		}
		p.pc.Close()
		p.closeIncoming()
	})
	return nil
}

// BytesSent reports application bytes written to the DataChannel.
func (p *Peer) BytesSent() int64 { return p.bytesSent.Load() }

// BytesReceived reports application bytes read from the DataChannel.
func (p *Peer) BytesReceived() int64 { return p.bytesReceived.Load() }
