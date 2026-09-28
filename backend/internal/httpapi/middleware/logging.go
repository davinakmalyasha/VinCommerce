package middleware

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// statusRecorder captures the response code and byte count for access logs.
// It forwards the optional http.ResponseWriter capabilities so streaming
// (SSE) and connection-upgrade endpoints keep working through the chain.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush supports streaming responses (SSE) through the middleware chain.
func (r *statusRecorder) Flush() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack supports connection upgrades (websockets).
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Push supports HTTP/2 server push.
func (r *statusRecorder) Push(target string, opts *http.PushOptions) error {
	if p, ok := r.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

// Logging records structured access logs for every request.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			// A handler that wrote nothing implicitly responded 200; a panic
			// that unwound past WriteHeader is reported as a 5xx rather than
			// a bogus 0.
			status := rec.status
			if !rec.wroteHeader {
				status = http.StatusInternalServerError
			}

			// Probes and scrapes are high-frequency and low-value; keep them
			// out of the default Info stream.
			lvl := slog.LevelInfo
			switch {
			case strings.HasPrefix(r.URL.Path, "/health"), r.URL.Path == "/metrics":
				lvl = slog.LevelDebug
			}

			logger.Log(r.Context(), lvl, "http_request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"bytes", rec.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
				"request_id", RequestIDFrom(r.Context()),
				"user_agent", r.UserAgent(),
			)
		})
	}
}
