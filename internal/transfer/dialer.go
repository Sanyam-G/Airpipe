package transfer

import (
	"time"

	"github.com/gorilla/websocket"
)

// relayDialer bounds the handshake so a dead relay fails fast.
var relayDialer = &websocket.Dialer{
	Proxy:            websocket.DefaultDialer.Proxy,
	HandshakeTimeout: 15 * time.Second,
}
