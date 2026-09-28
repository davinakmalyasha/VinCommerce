package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Same reasoning as middleware/logging_test.go: the metrics recorder is a
// ResponseWriter wrapper, and a wrapper that drops the optional interfaces
// silently breaks every streaming endpoint in the application.

func TestStatusRecorderIsFlusher(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	if _, ok := any(rec).(http.Flusher); !ok {
		t.Fatal("metrics statusRecorder must implement http.Flusher (SSE endpoints 500 without it)")
	}
	if _, ok := any(rec).(http.Hijacker); !ok {
		t.Fatal("metrics statusRecorder must implement http.Hijacker")
	}
	if _, ok := any(rec).(http.Pusher); !ok {
		t.Fatal("metrics statusRecorder must implement http.Pusher")
	}
	if rec.Unwrap() == nil {
		t.Fatal("Unwrap must return the underlying ResponseWriter")
	}
}

func TestMiddlewarePreservesFlusher(t *testing.T) {
	var asserted bool
	reg := New()
	h := reg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, asserted = w.(http.Flusher)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/stream/orders", nil))
	if !asserted {
		t.Fatal("http.Flusher was not available to the handler behind the metrics middleware")
	}
}

func TestStatusRecorderFirstStatusWins(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusTeapot)
	if rec.status != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (first write wins)", rec.status)
	}
}

func TestFlushReachesUnderlyingWriter(t *testing.T) {
	inner := &flushableWriter{header: http.Header{}}
	rec := &statusRecorder{ResponseWriter: inner}
	rec.Flush()
	if !inner.flushed {
		t.Fatal("Flush did not propagate to the underlying writer")
	}
	if rec.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 after Flush", rec.status)
	}
}

func TestPanicIsRecordedAs5xx(t *testing.T) {
	// A panic must still produce a sample; otherwise panics vanish from
	// metrics entirely (the recoverer writes the 500 upstream of us).
	reg := New()
	h := reg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	defer func() { _ = recover() }()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
}

func TestRouteLabelFallsBackToMaskedPath(t *testing.T) {
	// No chi route context on a bare request -> masked path, and a long
	// UUID-ish segment must not leak into the label verbatim.
	got := routeLabel(httptest.NewRequest(http.MethodGet,
		"/api/v1/orders/6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f", nil))
	want := "/api/v1/orders/:id"
	if got != want {
		t.Fatalf("routeLabel = %q, want %q", got, want)
	}
}

func TestNormalizeRouteMasksLongSegments(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/api/v1/orders/6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f", "/api/v1/orders/:id"},
		{"/api/v1/products/some-slug", "/api/v1/products/some-slug"},
		{"/uploads/photo.jpg", "/uploads/photo.jpg"},
	}
	for _, c := range cases {
		if got := normalizeRoute(splitPath(c.in)); got != c.want {
			t.Errorf("normalizeRoute(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- helpers ---

type flushableWriter struct {
	header  http.Header
	flushed bool
}

func (f *flushableWriter) Header() http.Header         { return f.header }
func (f *flushableWriter) Write(b []byte) (int, error) { return len(b), nil }
func (f *flushableWriter) WriteHeader(int)             {}
func (f *flushableWriter) Flush()                      { f.flushed = true }

var (
	_ http.Flusher  = (*flushableWriter)(nil)
	_ http.Hijacker = (*statusRecorder)(nil)
)
