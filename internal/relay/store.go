package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var errTokenExists = errors.New("token already exists")

type StoredFile struct {
	Path      string
	Filename  string
	Size      int64
	CreatedAt time.Time
}

// FileStore holds encrypted mailbox blobs on disk until they expire.
type FileStore struct {
	mu           sync.RWMutex
	files        map[string]*StoredFile
	dir          string
	expiry       time.Duration
	log          *slog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	expiredTotal atomic.Int64
}

func NewFileStore(parent context.Context, log *slog.Logger, expiry time.Duration) (*FileStore, error) {
	dir, err := os.MkdirTemp("", "airpipe-*")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	fs := &FileStore{
		files:  make(map[string]*StoredFile),
		dir:    dir,
		expiry: expiry,
		log:    log,
		ctx:    ctx,
		cancel: cancel,
	}
	go fs.cleanupLoop()
	return fs, nil
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
	// Re-check under the write lock: two uploads racing the same client token
	// both pass the RLock precheck above.
	if _, exists := fs.files[token]; exists {
		fs.mu.Unlock()
		os.Remove(tmp.Name())
		return "", errTokenExists
	}
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

func (fs *FileStore) ExpiredTotal() int64 {
	return fs.expiredTotal.Load()
}

func (fs *FileStore) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			fs.mu.Lock()
			for token, f := range fs.files {
				if time.Since(f.CreatedAt) > fs.expiry {
					os.Remove(f.Path)
					delete(fs.files, token)
					fs.expiredTotal.Add(1)
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
