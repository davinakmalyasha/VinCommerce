package metrics

import (
	"bufio"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds the application metrics.
type Registry struct {
	reg *prometheus.Registry

	httpDuration *prometheus.HistogramVec
	httpRequests *prometheus.CounterVec
}

// New creates the metrics registry with default + application collectors.
func New() *Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	r := &Registry{
		reg: reg,
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "vc_http_request_duration_seconds",
			Help:    "API request latency by route pattern and status.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		}, []string{"method", "route", "status"}),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vc_http_requests_total",
			Help: "Total API requests by method, route and status.",
		}, []string{"method", "route", "status"}),
	}
	reg.MustRegister(r.httpDuration)
	reg.MustRegister(r.httpRequests)
	return r
}

// statusRecorder captures the response code for instrumentation.
//
// It deliberately forwards the optional http.ResponseWriter capabilities
// (Flusher, Hijacker, Pusher, ReaderFrom). Embedding the http.ResponseWriter
// *interface* only promotes Header/Write/WriteHeader, so without these
// methods a `w.(http.Flusher)` assertion inside a handler fails and any
// streaming endpoint (SSE) degrades to a 500. See /stream handlers.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush implements http.Flusher, required for server-sent events.
func (w *statusRecorder) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker for connection upgrades (websockets).
func (w *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Push implements http.Pusher for HTTP/2 server push.
func (w *statusRecorder) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

// Middleware instruments every API request with latency/count metrics.
// Route labels use the chi RoutePattern (e.g. /orders/{id}), avoiding
// high-cardinality label explosion.
func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// Deferred so a panic is still accounted for as a 5xx rather than
		// silently dropping the sample.
		defer func() {
			if !rec.wroteHeader {
				// Nothing wrote a status: recoverer will emit 500 upstream.
				rec.status = http.StatusInternalServerError
			}
			route := routeLabel(req)
			status := strconv.Itoa(rec.status)

			r.httpRequests.WithLabelValues(req.Method, route, status).Inc()
			r.httpDuration.WithLabelValues(req.Method, route, status).
				Observe(time.Since(start).Seconds())
		}()

		next.ServeHTTP(rec, req)
	})
}

// routeLabel prefers the chi route pattern (bounded cardinality) and falls
// back to a masked URL path when routing has not resolved a pattern yet.
func routeLabel(req *http.Request) string {
	if rctx := chi.RouteContext(req.Context()); rctx != nil {
		if pattern := rctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	route := strings.TrimSuffix(req.URL.Path, "/")
	if parts := splitPath(route); len(parts) > 0 {
		return normalizeRoute(parts)
	}
	return route
}

func splitPath(p string) []string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		return nil
	}
	return parts
}

// normalizeRoute masks long UUID-ish segments so per-object paths aggregate.
func normalizeRoute(parts []string) string {
	out := make([]string, len(parts))
	for i, p := range parts {
		if len(p) >= 16 && !strings.Contains(p, ".") { // uuid-like segment
			out[i] = ":id"
		} else {
			out[i] = p
		}
	}
	return "/" + strings.Join(out, "/")
}

// Handler exposes /metrics.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}
