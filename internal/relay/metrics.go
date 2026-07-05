package relay

import (
	"fmt"
	"net/http"
	"time"

	"github.com/sanyamgarg/airpipe/internal/transfer"
)

// handleMetrics serves Prometheus text format without a client dependency.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	fileCount, fileBytes := s.fileStore.Stats()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	gauge := func(name, help string, value any) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %v\n", name, help, name, name, value)
	}
	counter := func(name, help string, value int64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, value)
	}

	fmt.Fprintf(w, "# HELP airpipe_build_info Build metadata.\n# TYPE airpipe_build_info gauge\nairpipe_build_info{version=%q,protocol=\"%d\"} 1\n",
		s.version, int(transfer.ProtocolVersion))
	gauge("airpipe_uptime_seconds", "Seconds since the relay started.", int(time.Since(s.startedAt).Seconds()))
	gauge("airpipe_active_files", "Mailbox blobs currently held.", fileCount)
	gauge("airpipe_active_file_bytes", "Total bytes of mailbox blobs currently held.", fileBytes)
	gauge("airpipe_active_ws_rooms", "Live WebSocket rooms.", s.roomManager.ActiveRooms())
	counter("airpipe_uploads_total", "Mailbox uploads accepted.", s.uploadsTotal.Load())
	counter("airpipe_upload_bytes_total", "Bytes accepted into the mailbox.", s.uploadBytesTotal.Load())
	counter("airpipe_downloads_total", "Mailbox downloads served.", s.downloadsTotal.Load())
	counter("airpipe_download_bytes_total", "Bytes served from the mailbox.", s.downloadBytesTotal.Load())
	counter("airpipe_ws_connections_total", "WebSocket clients that joined a room.", s.wsConnectionsTotal.Load())
	counter("airpipe_rate_limited_total", "Requests rejected by the per-IP rate limit.", s.rateLimitedTotal.Load())
	counter("airpipe_expired_files_total", "Mailbox blobs deleted by expiry.", s.fileStore.ExpiredTotal())
}
