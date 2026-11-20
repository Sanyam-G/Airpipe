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

var (
	errTokenExists = errors.New("token already exists")
	errStoreFull   = errors.New("relay storage is full")
)

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
	maxBytes     int64        // cap on stored blobs plus uploads in flight
	used         atomic.Int64 // bytes reserved against maxBytes
	log          *slog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	expiredTotal atomic.Int64
}

func NewFileStore(parent context.Context, log *slog.Logger, expiry time.Duration, maxBytes int64) (*FileStore, error) {
	dir, err := os.MkdirTemp("", "airpipe-*")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	fs := &FileStore{
		files:    make(map[string]*StoredFile),
		dir:      dir,
		expiry:   expiry,
		maxBytes: maxBytes,
		log:      log,
		ctx:      ctx,
		cancel:   cancel,
	}
	go fs.cleanupLoop()
	return fs, nil
}

// Reserve claims n bytes of the store's capacity, or reports that it is full.
// The caller releases whatever it does not leave stored.
func (fs *FileStore) Reserve(n int64) bool {
	if fs.used.Add(n) > fs.maxBytes {
		fs.used.Add(-n)
		return false
	}
	return true
}

func (fs *FileStore) Release(n int64) {
	fs.used.Add(-n)
}

// quotaReader reserves store capacity for each byte it reads and fails once
// the store is full. n is what it holds reserved.
type quotaReader struct {
	io.ReadCloser
	store *FileStore
	n     int64
	full  bool
}

func (q *quotaReader) Read(p []byte) (int, error) {
	n, err := q.ReadCloser.Read(p)
	if n > 0 {
		if !q.store.Reserve(int64(n)) {
			q.full = true
			return 0, errStoreFull
		}
		q.n += int64(n)
	}
	return n, err
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
					fs.Release(f.Size)
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
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func shortToken(t string) string {
	if len(t) <= 4 {
		return t
	}
	return t[:4]
}
