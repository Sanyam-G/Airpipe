package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

var defaultICEServers = []webrtc.ICEServer{
	{URLs: []string{"stun:stun.l.google.com:19302"}},
	{URLs: []string{"stun:stun1.l.google.com:19302"}},
	{URLs: []string{"stun:stun.cloudflare.com:3478"}},
}

const backpressureHighMark = 8 << 20

type Role int

const (
	RoleOfferer Role = iota
	RoleAnswerer
)

type Peer struct {
	pc   *webrtc.PeerConnection
	dc   *webrtc.DataChannel
	role Role

	localCandidates chan webrtc.ICECandidateInit
	dataChannelOpen chan struct{}
	incoming        chan []byte

	closed     chan struct{}
	closeOnce  sync.Once
	incomingMu sync.Mutex
	incClosed  bool
}

func NewPeer(role Role) (*Peer, error) {
	api := webrtc.NewAPI()
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: defaultICEServers})
	if err != nil {
		return nil, fmt.Errorf("new peer connection: %w", err)
	}

	p := &Peer{
		pc:              pc,
		role:            role,
		localCandidates: make(chan webrtc.ICECandidateInit, 64),
		dataChannelOpen: make(chan struct{}),
		incoming:        make(chan []byte, 32),
		closed:          make(chan struct{}),
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		// Drop on full buffer. Blocking here stalls pion's ICE agent and
		// eventually kills DTLS/SCTP consent.
		select {
		case p.localCandidates <- c.ToJSON():
		default:
		}
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			p.Close()
		}
	})

	// Negotiated DataChannel: both sides create with the same ID, no OnDataChannel
	// needed. Avoids the race where offerer's OnOpen fires before answerer registers.
	ordered := true
	dcID := uint16(1)
	negotiated := true
	dc, dcErr := pc.CreateDataChannel("airpipe", &webrtc.DataChannelInit{
		Ordered:    &ordered,
		ID:         &dcID,
		Negotiated: &negotiated,
	})
	if dcErr != nil {
		pc.Close()
		return nil, fmt.Errorf("create data channel: %w", dcErr)
	}
	p.attachDataChannel(dc)

	return p, nil
}

func (p *Peer) attachDataChannel(dc *webrtc.DataChannel) {
	p.dc = dc
	var openOnce sync.Once
	dc.OnOpen(func() {
		openOnce.Do(func() { close(p.dataChannelOpen) })
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// Pion reuses the buffer between callbacks.
		data := make([]byte, len(msg.Data))
		copy(data, msg.Data)
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

func (p *Peer) SetRemoteAnswer(ctx context.Context, sdp string) error {
	return p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	})
}

func (p *Peer) AddICECandidate(raw []byte) error {
	var c webrtc.ICECandidateInit
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("decode ICE candidate: %w", err)
	}
	return p.pc.AddICECandidate(c)
}

func (p *Peer) LocalICECandidates() <-chan webrtc.ICECandidateInit {
	return p.localCandidates
}

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

func (p *Peer) Send(data []byte) error {
	if p.dc == nil {
		return errors.New("datachannel not ready")
	}
	for p.dc.BufferedAmount() > backpressureHighMark {
		select {
		case <-time.After(5 * time.Millisecond):
		case <-p.closed:
			return errors.New("peer closed")
		}
	}
	if err := p.dc.Send(data); err != nil {
		return fmt.Errorf("dc send: %w", err)
	}
	return nil
}

func (p *Peer) Messages() <-chan []byte { return p.incoming }

func (p *Peer) Closed() <-chan struct{} { return p.closed }

func (p *Peer) IsOpen() bool {
	select {
	case <-p.dataChannelOpen:
		return true
	default:
		return false
	}
}

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

// WaitDrain blocks until the send buffer is empty, the timeout elapses, or
// the peer closes. Call before Close() so the Complete message isn't dropped.
func (p *Peer) WaitDrain(timeout time.Duration) {
	if p.dc == nil {
		return
	}
	deadline := time.Now().Add(timeout)
	for p.dc.BufferedAmount() > 0 {
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-p.closed:
			return
		}
	}
}
