package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sanyamgarg/airpipe/internal/transfer"
	"golang.org/x/time/rate"
)

//go:embed static/*
var staticFiles embed.FS

// buildVersion is set via -ldflags at build time.
var buildVersion = "dev"

var startedAt = time.Now()

type config struct {
	port            string
	allowedOrigins  []string
	allowAnyOrigin  bool
	rateLimitPerMin int
	logFormat       string
}

func loadConfig() config {
	c := config{
		port:            getenv("PORT", "8080"),
		rateLimitPerMin: getenvInt("AIRPIPE_RATE_LIMIT_PER_MIN", 60),
		logFormat:       getenv("AIRPIPE_LOG_FORMAT", "json"),
	}
	raw := strings.TrimSpace(os.Getenv("AIRPIPE_ALLOWED_ORIGINS"))
	if raw == "" {
		c.allowedOrigins = []string{
			"https://airpipe.sanyamgarg.com",
			"http://localhost:8080",
			"http://127.0.0.1:8080",
		}
	} else if raw == "*" {
		c.allowAnyOrigin = true
	} else {
		for _, o := range strings.Split(raw, ",") {
			if v := strings.TrimSpace(o); v != "" {
				c.allowedOrigins = append(c.allowedOrigins, v)
			}
		}
	}
	return c
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func newLogger(format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if format == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

const (
	maxUploadSize = 500 << 20
	fileExpiry    = 10 * time.Minute
)

var (
	errTokenExists = errors.New("token already exists")
	validToken     = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

type StoredFile struct {
	Path      string
	Filename  string
	Size      int64
	CreatedAt time.Time
}

type FileStore struct {
	mu     sync.RWMutex
	files  map[string]*StoredFile
	dir    string
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
}

func NewFileStore(parent context.Context, log *slog.Logger) *FileStore {
	dir, err := os.MkdirTemp("", "airpipe-*")
	if err != nil {
		log.Error("create temp dir failed", "err", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(parent)
	fs := &FileStore{
		files:  make(map[string]*StoredFile),
		dir:    dir,
		log:    log,
		ctx:    ctx,
		cancel: cancel,
	}
	go fs.cleanupLoop()
	return fs
}

func (fs *FileStore) Store(filename string, r io.Reader, clientToken string) (string, error) {
	token := clientToken
	if token == "" {
		token = genToken()
	}

	fs.mu.RLock()
	_, exists := fs.files[token]
	fs.mu.RUnlock()
	if exists {
		return "", errTokenExists
	}

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

	fs.log.Info("file stored", "token", shortToken(token), "bytes", size)
	return token, nil
}

func (fs *FileStore) Get(token string) (*StoredFile, bool) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	f, ok := fs.files[token]
	return f, ok
}

func (fs *FileStore) Stats() (count int, totalBytes int64) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	for _, f := range fs.files {
		count++
		totalBytes += f.Size
	}
	return
}

func (fs *FileStore) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			fs.mu.Lock()
			for token, f := range fs.files {
				if time.Since(f.CreatedAt) > fileExpiry {
					os.Remove(f.Path)
					delete(fs.files, token)
					fs.log.Info("file expired", "token", shortToken(token))
				}
			}
			fs.mu.Unlock()
		case <-fs.ctx.Done():
			return
		}
	}
}

func (fs *FileStore) Shutdown() {
	fs.cancel()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, f := range fs.files {
		os.Remove(f.Path)
	}
	os.RemoveAll(fs.dir)
}

func genToken() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func shortToken(t string) string {
	if len(t) <= 4 {
		return t
	}
	return t[:4]
}

type Room struct {
	token     string
	clients   []*websocket.Conn
	mu        sync.Mutex
	createdAt time.Time
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
	room := &Room{token: token, clients: make([]*websocket.Conn, 0, 2), createdAt: time.Now()}
	rm.rooms[token] = room
	return room
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
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
				conn.Close()
			}
		}
	}
}

type ipLimiter struct {
	mu       sync.Mutex
	clients  map[string]*rate.Limiter
	perMin   int
	cleanup  time.Time
	perIPTTL time.Duration
}

func newIPLimiter(perMin int) *ipLimiter {
	return &ipLimiter{
		clients:  make(map[string]*rate.Limiter),
		perMin:   perMin,
		perIPTTL: 10 * time.Minute,
	}
}

func (il *ipLimiter) allow(ip string) bool {
	il.mu.Lock()
	defer il.mu.Unlock()
	if time.Since(il.cleanup) > il.perIPTTL {
		il.clients = make(map[string]*rate.Limiter)
		il.cleanup = time.Now()
	}
	lim, ok := il.clients[ip]
	if !ok {
		lim = rate.NewLimiter(rate.Limit(float64(il.perMin)/60.0), il.perMin)
		il.clients[ip] = lim
	}
	return lim.Allow()
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("Cf-Connecting-Ip"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.Index(v, ","); i != -1 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func rateLimit(il *ipLimiter, log *slog.Logger, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !il.allow(ip) {
			log.Warn("rate limited", "ip", ip, "path", r.URL.Path)
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func originChecker(cfg config, log *slog.Logger) func(*http.Request) bool {
	return func(r *http.Request) bool {
		if cfg.allowAnyOrigin {
			return true
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			// CLI clients have no Origin header.
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			log.Warn("invalid ws origin", "origin", origin)
			return false
		}
		origin = strings.ToLower(u.Scheme + "://" + u.Host)
		for _, allowed := range cfg.allowedOrigins {
			if strings.EqualFold(origin, allowed) {
				return true
			}
		}
		log.Warn("rejected ws origin", "origin", origin)
		return false
	}
}

type server struct {
	cfg         config
	log         *slog.Logger
	fileStore   *FileStore
	roomManager *RoomManager
	upgrader    websocket.Upgrader
	rl          *ipLimiter
}

func (s *server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":    token,
		"filename": header.Filename,
	})
}

func (s *server) handleDownloadPage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	if _, ok := s.fileStore.Get(token); !ok {
		http.Error(w, "not found or expired", http.StatusNotFound)
		return
	}
	writeStatic(w, "download.html")
}

func (s *server) handleRawDownload(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", sf.CreatedAt, f)
}

func (s *server) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	writeStatic(w, "sender.html")
}

func (s *server) handleLiveSendPage(w http.ResponseWriter, r *http.Request) {
	writeStatic(w, "live-send.html")
}

func (s *server) handleLiveReceivePage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" || !validToken.MatchString(token) {
		http.Error(w, "invalid token", http.StatusBadRequest)
		return
	}
	writeStatic(w, "live-receive.html")
}

func (s *server) handleLandingPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeStatic(w, "index.html")
}

func (s *server) handleSendPage(w http.ResponseWriter, r *http.Request) {
	writeStatic(w, "send.html")
}

func (s *server) handleInstall(w http.ResponseWriter, r *http.Request) {
	writeStaticContentType(w, "install.sh", "text/plain; charset=utf-8")
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	fileCount, bytes := s.fileStore.Stats()
	rooms := s.roomManager.ActiveRooms()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":           "ok",
		"version":          buildVersion,
		"uptime_seconds":   int(time.Since(startedAt).Seconds()),
		"active_files":     fileCount,
		"active_bytes":     bytes,
		"active_ws_rooms":  rooms,
		"protocol_version": int(transfer.ProtocolVersion),
	})
}

func writeStatic(w http.ResponseWriter, name string) {
	writeStaticContentType(w, name, "text/html; charset=utf-8")
}

func writeStaticContentType(w http.ResponseWriter, name, contentType string) {
	staticFS, _ := fs.Sub(staticFiles, "static")
	content, err := fs.ReadFile(staticFS, name)
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(content)
}

func main() {
	cfg := loadConfig()
	log := newLogger(cfg.logFormat)

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s := &server{
		cfg:         cfg,
		log:         log,
		fileStore:   NewFileStore(rootCtx, log),
		roomManager: NewRoomManager(rootCtx, log),
		rl:          newIPLimiter(cfg.rateLimitPerMin),
	}
	s.upgrader = websocket.Upgrader{
		CheckOrigin: originChecker(cfg, log),
	}
	defer s.fileStore.Shutdown()
	defer s.roomManager.Shutdown()

	mux := http.NewServeMux()
	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /", s.handleLandingPage)
	mux.HandleFunc("GET /send", s.handleSendPage)
	mux.HandleFunc("GET /install.sh", s.handleInstall)
	mux.HandleFunc("POST /upload", rateLimit(s.rl, log, s.handleUploadFile))
	mux.HandleFunc("GET /d/{token}", s.handleDownloadPage)
	mux.HandleFunc("GET /raw/{token}", s.handleRawDownload)
	mux.HandleFunc("GET /u/{token}", s.handleUploadPage)
	mux.HandleFunc("GET /live", s.handleLiveSendPage)
	mux.HandleFunc("GET /live/{token}", s.handleLiveReceivePage)
	mux.HandleFunc("GET /ws/{token}", rateLimit(s.rl, log, s.handleWebSocket))
	mux.HandleFunc("GET /health", s.handleHealth)

	srv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("relay starting",
			"port", cfg.port,
			"version", buildVersion,
			"allowed_origins", cfg.allowedOrigins,
			"allow_any_origin", cfg.allowAnyOrigin,
			"rate_limit_per_min", cfg.rateLimitPerMin,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	<-rootCtx.Done()
	log.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
	log.Info("shutdown complete")
}
