package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io"
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

// --- File Store (for HTTP upload/download) ---

const (
	maxUploadSize = 500 << 20 // 500MB
	fileExpiry    = 10 * time.Minute
)

type StoredFile struct {
	Path      string
	Filename  string
	Size      int64
	CreatedAt time.Time
}

type FileStore struct {
	mu    sync.RWMutex
	files map[string]*StoredFile
	dir   string
}

func NewFileStore() *FileStore {
	dir, err := os.MkdirTemp("", "airpipe-*")
	if err != nil {
		log.Fatalf("failed to create temp dir: %v", err)
	}
	fs := &FileStore{files: make(map[string]*StoredFile), dir: dir}
	go fs.cleanupLoop()
	return fs
}

func (fs *FileStore) Store(filename string, r io.Reader) (string, error) {
	token := genToken()

	tmp, err := os.CreateTemp(fs.dir, "upload-*")
	if err != nil {
		return "", err
	}

	size, err := io.Copy(tmp, r)
	tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	fs.mu.Lock()
	fs.files[token] = &StoredFile{
		Path:      tmp.Name(),
		Filename:  filename,
		Size:      size,
		CreatedAt: time.Now(),
	}
	fs.mu.Unlock()

	log.Printf("stored file %q (%d bytes) as %s", filename, size, token)
	return token, nil
}

func (fs *FileStore) Get(token string) (*StoredFile, bool) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	f, ok := fs.files[token]
	return f, ok
}

func (fs *FileStore) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		fs.mu.Lock()
		for token, f := range fs.files {
			if time.Since(f.CreatedAt) > fileExpiry {
				os.Remove(f.Path)
				delete(fs.files, token)
				log.Printf("expired file %s", token)
			}
		}
		fs.mu.Unlock()
	}
}

func genToken() string {
	b := make([]byte, 3)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- WebSocket Rooms (for receive flow) ---

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

// --- Globals ---

var (
	roomManager = NewRoomManager()
	fileStore   = NewFileStore()
)

// --- Handlers ---

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

func handleUploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "upload failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	token, err := fileStore.Store(header.Filename, file)
	if err != nil {
		http.Error(w, "storage failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":    token,
		"filename": header.Filename,
	})
}

func handleDownloadPage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	if _, ok := fileStore.Get(token); !ok {
		http.Error(w, "not found or expired", http.StatusNotFound)
		return
	}

	staticFS, _ := fs.Sub(staticFiles, "static")
	content, err := fs.ReadFile(staticFS, "download.html")
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

func handleRawDownload(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	sf, ok := fileStore.Get(token)
	if !ok {
		http.Error(w, "not found or expired", http.StatusNotFound)
		return
	}

	f, err := os.Open(sf.Path)
	if err != nil {
		http.Error(w, "file unavailable", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", sf.CreatedAt, f)
}

func handleUploadPage(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("POST /upload", handleUploadFile)
	mux.HandleFunc("GET /d/{token}", handleDownloadPage)
	mux.HandleFunc("GET /raw/{token}", handleRawDownload)
	mux.HandleFunc("GET /u/{token}", handleUploadPage)
	mux.HandleFunc("GET /ws/{token}", handleWebSocket)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	})
	log.Printf("relay starting on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
