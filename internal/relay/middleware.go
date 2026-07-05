package relay

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

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

func (s *Server) rateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !s.rl.allow(ip) {
			s.rateLimitedTotal.Add(1)
			s.log.Warn("rate limited", "ip", ip, "path", r.URL.Path)
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func originChecker(cfg Config, log *slog.Logger) func(*http.Request) bool {
	return func(r *http.Request) bool {
		if cfg.AllowAnyOrigin {
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
		// Same-origin is always fine: pages served by this relay may talk to
		// it whatever host/port it runs on.
		if strings.EqualFold(u.Host, r.Host) {
			return true
		}
		origin = strings.ToLower(u.Scheme + "://" + u.Host)
		for _, allowed := range cfg.AllowedOrigins {
			if strings.EqualFold(origin, allowed) {
				return true
			}
		}
		log.Warn("rejected ws origin", "origin", origin)
		return false
	}
}
