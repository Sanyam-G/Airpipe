package relay

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Room pairs exactly two WebSocket clients and relays binary frames between them.
type Room struct {
	token        string
	clients      []*websocket.Conn
	mu           sync.Mutex
	lastActivity time.Time
}

type RoomManager struct {
	rooms  map[string]*Room
	mu     sync.RWMutex
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
}

func NewRoomManager(parent context.Context, log *slog.Logger) *RoomManager {
	ctx, cancel := context.WithCancel(parent)
	rm := &RoomManager{
		rooms:  make(map[string]*Room),
		log:    log,
		ctx:    ctx,
		cancel: cancel,
	}
	go rm.cleanupLoop()
	return rm
}

func (rm *RoomManager) GetOrCreateRoom(token string) *Room {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if room, exists := rm.rooms[token]; exists {
		return room
	}
	room := &Room{token: token, clients: make([]*websocket.Conn, 0, 2), lastActivity: time.Now()}
	rm.rooms[token] = room
	return room
}

// Waiting reports whether a room exists with exactly one connected client.
func (rm *RoomManager) Waiting(token string) bool {
	rm.mu.RLock()
	room, exists := rm.rooms[token]
	rm.mu.RUnlock()
	if !exists {
		return false
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	return len(room.clients) == 1
}

func (rm *RoomManager) DeleteRoom(token string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	delete(rm.rooms, token)
}

func (rm *RoomManager) ActiveRooms() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.rooms)
}

func (rm *RoomManager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rm.mu.Lock()
			for token, room := range rm.rooms {
				room.mu.Lock()
				idle := time.Since(room.lastActivity)
				if idle > 10*time.Minute {
					for _, conn := range room.clients {
						conn.Close()
					}
					room.mu.Unlock()
					delete(rm.rooms, token)
					continue
				}
				room.mu.Unlock()
			}
			rm.mu.Unlock()
		case <-rm.ctx.Done():
			return
		}
	}
}

func (rm *RoomManager) Shutdown() {
	rm.cancel()
	rm.mu.Lock()
	defer rm.mu.Unlock()
	for _, room := range rm.rooms {
		room.mu.Lock()
		for _, conn := range room.clients {
			conn.Close()
		}
		room.mu.Unlock()
	}
}

func (room *Room) AddClient(conn *websocket.Conn) bool {
	room.mu.Lock()
	defer room.mu.Unlock()
	if len(room.clients) >= 2 {
		return false
	}
	room.clients = append(room.clients, conn)
	room.lastActivity = time.Now()
	return true
}

// RemoveClient drops conn and kicks any remaining peer.
func (room *Room) RemoveClient(conn *websocket.Conn) {
	room.mu.Lock()
	nBefore := len(room.clients)
	idx := -1
	for i, c := range room.clients {
		if c == conn {
			idx = i
			break
		}
	}
	if idx < 0 {
		room.mu.Unlock()
		return
	}
	room.clients = append(room.clients[:idx], room.clients[idx+1:]...)

	var toKick []*websocket.Conn
	if nBefore == 2 && len(room.clients) == 1 {
		toKick = append(toKick, room.clients[0])
		room.clients = room.clients[:0]
	}
	room.mu.Unlock()

	for _, oc := range toKick {
		_ = oc.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "peer disconnected"))
		oc.Close()
	}
}

func (room *Room) Broadcast(sender *websocket.Conn, message []byte) {
	room.mu.Lock()
	defer room.mu.Unlock()
	room.lastActivity = time.Now()
	for _, conn := range room.clients {
		if conn != sender {
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
				conn.Close()
			}
		}
	}
}
