package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

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
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Middleware instruments every API request with latency/count metrics.
// Route labels use the chi RoutePattern (e.g. /orders/{id}), avoiding
// high-cardinality label explosion.
func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, req)

		route := strings.TrimSuffix(req.URL.Path, "/")
		if parts := splitPath(route); len(parts) > 0 {
			route = normalizeRoute(parts)
		}
		status := strconv.Itoa(rec.status)

		r.httpRequests.WithLabelValues(req.Method, route, status).Inc()
		r.httpDuration.WithLabelValues(req.Method, route, status).
			Observe(time.Since(start).Seconds())
	})
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
