package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sanyamgarg/airpipe/internal/transfer"
	"github.com/sanyamgarg/airpipe/web"
)

var validToken = regexp.MustCompile(`^[0-9a-f]{16}$`)

type Server struct {
	cfg         Config
	log         *slog.Logger
	version     string
	startedAt   time.Time
	fileStore   *FileStore
	roomManager *RoomManager
	upgrader    websocket.Upgrader
	rl          *ipLimiter

	uploadsTotal       atomic.Int64
	uploadBytesTotal   atomic.Int64
	downloadsTotal     atomic.Int64
	downloadBytesTotal atomic.Int64
	wsConnectionsTotal atomic.Int64
	rateLimitedTotal   atomic.Int64
}

func New(parent context.Context, cfg Config, log *slog.Logger, version string) (*Server, error) {
	store, err := NewFileStore(parent, log, cfg.FileExpiry)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:         cfg,
		log:         log,
		version:     version,
		startedAt:   time.Now(),
		fileStore:   store,
		roomManager: NewRoomManager(parent, log),
		rl:          newIPLimiter(cfg.RateLimitPerMin),
	}
	s.upgrader = websocket.Upgrader{CheckOrigin: originChecker(cfg, log)}
	return s, nil
}

func (s *Server) Shutdown() {
	s.fileStore.Shutdown()
	s.roomManager.Shutdown()
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(StaticFS()))))
	mux.Handle("GET /site/", http.StripPrefix("/site/", http.FileServer(http.FS(web.FS()))))
	mux.HandleFunc("GET /", s.handleLandingPage)
	mux.HandleFunc("GET /development", s.handleDevelopmentPage)
	mux.HandleFunc("GET /send", s.handleSendPage)
	mux.HandleFunc("GET /install.sh", s.handleInstall)
	mux.HandleFunc("POST /upload", s.rateLimit(s.handleUploadFile))
	mux.HandleFunc("GET /d/{token}", s.handleDownloadPage)
	mux.HandleFunc("GET /raw/{token}", s.handleRawDownload)
	mux.HandleFunc("GET /u/{token}", s.handleUploadPage)
	mux.HandleFunc("GET /live", s.handleLiveSendPage)
	mux.HandleFunc("GET /live/{token}", s.handleLiveReceivePage)
	mux.HandleFunc("GET /ws/{token}", s.rateLimit(s.handleWebSocket))
	mux.HandleFunc("GET /room/{token}", s.rateLimit(s.handleRoomStatus))
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	return mux
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("websocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	room := s.roomManager.GetOrCreateRoom(token)
	if !room.AddClient(conn) {
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "room full"))
		return
	}
	defer room.RemoveClient(conn)

	s.wsConnectionsTotal.Add(1)
	s.log.Info("client joined room", "token", shortToken(token), "ip", clientIP(r))

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
		s.roomManager.DeleteRoom(token)
	}
}

func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes)

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "upload failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	clientToken := r.FormValue("token")
	if clientToken != "" && !validToken.MatchString(clientToken) {
		http.Error(w, "invalid token format", http.StatusBadRequest)
		return
	}

	token, err := s.fileStore.Store(header.Filename, file, clientToken)
	if err != nil {
		if errors.Is(err, errTokenExists) {
			http.Error(w, "token conflict", http.StatusConflict)
			return
		}
		s.log.Error("store failed", "err", err)
		http.Error(w, "storage failed", http.StatusInternalServerError)
		return
	}

	if sf, ok := s.fileStore.Get(token); ok {
		s.uploadsTotal.Add(1)
		s.uploadBytesTotal.Add(sf.Size)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":    token,
		"filename": header.Filename,
	})
}

func (s *Server) handleDownloadPage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	// Always serve the page. It probes /raw first; if 404, it falls back to
	// joining the live WS room for passphrase-derived P2P pairing.
	writeStatic(w, "download.html")
}

func (s *Server) handleRawDownload(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	sf, ok := s.fileStore.Get(token)
	if !ok {
		http.Error(w, "not found or expired", http.StatusNotFound)
		return
	}
	f, err := os.Open(sf.Path)
	if err != nil {
		s.log.Error("open stored file", "err", err)
		http.Error(w, "file unavailable", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	if r.Method != http.MethodHead {
		s.downloadsTotal.Add(1)
		s.downloadBytesTotal.Add(sf.Size)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", sf.CreatedAt, f)
}

func (s *Server) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	writeStatic(w, "sender.html")
}

func (s *Server) handleLiveSendPage(w http.ResponseWriter, r *http.Request) {
	writeStatic(w, "live-send.html")
}

func (s *Server) handleLiveReceivePage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" || !validToken.MatchString(token) {
		http.Error(w, "invalid token", http.StatusBadRequest)
		return
	}
	writeStatic(w, "live-receive.html")
}

func (s *Server) handleLandingPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeFromFS(w, web.FS(), "index.html")
}

func (s *Server) handleDevelopmentPage(w http.ResponseWriter, r *http.Request) {
	writeFromFS(w, web.FS(), "development.html")
}

func (s *Server) handleSendPage(w http.ResponseWriter, r *http.Request) {
	writeStatic(w, "send.html")
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	content, err := fs.ReadFile(StaticFS(), "install.sh")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	out := strings.Replace(string(content), "__RELAY_URL__", scheme+"://"+r.Host, 1)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(out))
}

func (s *Server) handleRoomStatus(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if !validToken.MatchString(token) {
		http.Error(w, "invalid token", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"waiting": s.roomManager.Waiting(token),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	fileCount, bytes := s.fileStore.Stats()
	rooms := s.roomManager.ActiveRooms()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":           "ok",
		"version":          s.version,
		"uptime_seconds":   int(time.Since(s.startedAt).Seconds()),
		"active_files":     fileCount,
		"active_bytes":     bytes,
		"active_ws_rooms":  rooms,
		"protocol_version": int(transfer.ProtocolVersion),
		"max_upload_bytes": s.cfg.MaxUploadBytes,
		"expiry_seconds":   int(s.cfg.FileExpiry.Seconds()),
	})
}

func writeStatic(w http.ResponseWriter, name string) {
	content, err := fs.ReadFile(StaticFS(), name)
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

func writeFromFS(w http.ResponseWriter, fsys fs.FS, name string) {
	content, err := fs.ReadFile(fsys, name)
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}
