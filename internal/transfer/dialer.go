package transfer

import (
	"time"

	"github.com/gorilla/websocket"
)

// relayDialer bounds the WebSocket handshake so a dead or blackholed relay
// fails fast instead of hanging forever.
var relayDialer = &websocket.Dialer{
	Proxy:            websocket.DefaultDialer.Proxy,
	HandshakeTimeout: 15 * time.Second,
}
