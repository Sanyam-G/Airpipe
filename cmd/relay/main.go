package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed static/*
var staticFiles embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Room struct {
	token     string
	clients   []*websocket.Conn
	mu        sync.Mutex
	createdAt time.Time
}

type RoomManager struct {
	rooms map[string]*Room
	mu    sync.RWMutex
}

func NewRoomManager() *RoomManager {
	rm := &RoomManager{rooms: make(map[string]*Room)}
	go rm.cleanupLoop()
	return rm
}

func (rm *RoomManager) GetOrCreateRoom(token string) *Room {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if room, exists := rm.rooms[token]; exists {
		return room
	}
	room := &Room{token: token, clients: make([]*websocket.Conn, 0, 2), createdAt: time.Now()}
	rm.rooms[token] = room
	return room
}

func (rm *RoomManager) DeleteRoom(token string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	delete(rm.rooms, token)
}

func (rm *RoomManager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		rm.mu.Lock()
		for token, room := range rm.rooms {
			if time.Since(room.createdAt) > 10*time.Minute {
				room.mu.Lock()
				for _, conn := range room.clients {
					conn.Close()
				}
				room.mu.Unlock()
				delete(rm.rooms, token)
			}
		}
		rm.mu.Unlock()
	}
}

func (room *Room) AddClient(conn *websocket.Conn) bool {
	room.mu.Lock()
	defer room.mu.Unlock()
	if len(room.clients) >= 2 {
		return false
	}
	room.clients = append(room.clients, conn)
	return true
}

func (room *Room) RemoveClient(conn *websocket.Conn) {
	room.mu.Lock()
	defer room.mu.Unlock()
	for i, c := range room.clients {
		if c == conn {
			room.clients = append(room.clients[:i], room.clients[i+1:]...)
			break
		}
	}
}

func (room *Room) Broadcast(sender *websocket.Conn, message []byte) {
	room.mu.Lock()
	defer room.mu.Unlock()
	for _, conn := range room.clients {
		if conn != sender {
			conn.WriteMessage(websocket.BinaryMessage, message)
		}
	}
}

var roomManager = NewRoomManager()

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	room := roomManager.GetOrCreateRoom(token)
	if !room.AddClient(conn) {
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "room full"))
		return
	}
	defer room.RemoveClient(conn)

	log.Printf("client joined room %s", token)

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if messageType == websocket.BinaryMessage {
			room.Broadcast(conn, message)
		}
	}

	room.mu.Lock()
	isEmpty := len(room.clients) == 0
	room.mu.Unlock()
	if isEmpty {
		roomManager.DeleteRoom(token)
	}
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	staticFS, _ := fs.Sub(staticFiles, "static")
	content, err := fs.ReadFile(staticFS, "receiver.html")
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	staticFS, _ := fs.Sub(staticFiles, "static")
	content, err := fs.ReadFile(staticFS, "sender.html")
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/{token}", handleWebSocket)
	mux.HandleFunc("GET /d/{token}", handleDownload)
	mux.HandleFunc("GET /u/{token}", handleUpload)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	})
	log.Printf("relay starting on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
