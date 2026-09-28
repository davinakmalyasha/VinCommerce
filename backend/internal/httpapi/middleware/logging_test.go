package middleware

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The SSE handlers do `w.(http.Flusher)` and hard-fail with a 500 when the
// assertion fails. These tests pin that every response-writer wrapper in the
// chain keeps the optional http.ResponseWriter capabilities intact.

func TestLoggingRecorderIsFlusher(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	if _, ok := any(rec).(http.Flusher); !ok {
		t.Fatal("logging statusRecorder must implement http.Flusher (SSE endpoints 500 without it)")
	}
	if _, ok := any(rec).(http.Hijacker); !ok {
		t.Fatal("logging statusRecorder must implement http.Hijacker")
	}
	if _, ok := any(rec).(http.Pusher); !ok {
		t.Fatal("logging statusRecorder must implement http.Pusher")
	}
	if rec.Unwrap() == nil {
		t.Fatal("Unwrap must return the underlying ResponseWriter")
	}
}

func TestLoggingMiddlewarePreservesFlusher(t *testing.T) {
	// end-to-end: a handler behind the real middleware chain must still be
	// able to type-assert http.Flusher.
	var asserted bool
	h := Logging(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, asserted = w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/stream/orders", nil))
	if !asserted {
		t.Fatal("http.Flusher was not available to the handler behind Logging()")
	}
}

func TestStatusRecorderDefaultsToOK(t *testing.T) {
	// A handler that returns without writing must not report status 0.
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	rec.Write([]byte("body"))
	if rec.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.status)
	}
	if rec.bytes != 4 {
		t.Fatalf("bytes = %d, want 4", rec.bytes)
	}
	if !rec.wroteHeader {
		t.Fatal("Write must mark the header as written")
	}
}

func TestStatusRecorderFirstStatusWins(t *testing.T) {
	// A double WriteHeader is a caller bug, but the recorder must not let it
	// corrupt the logged status (and must not panic on a real ResponseWriter).
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusTeapot)
	if rec.status != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (first write wins)", rec.status)
	}
}

func TestStatusRecorderFlushImpliesOK(t *testing.T) {
	// SSE handlers flush before/without an explicit WriteHeader.
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rec.Flush()
	if rec.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 after Flush", rec.status)
	}
	if !rec.wroteHeader {
		t.Fatal("Flush must mark the header as written")
	}
}

func TestStatusRecorderUnwrapsToUnderlyingFlusher(t *testing.T) {
	// http.ResponseController walks Unwrap() to reach the real capabilities.
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	if got := rec.Unwrap(); got == nil {
		t.Fatal("nil underlying writer")
	}
	if err := http.NewResponseController(rec).Flush(); err != nil {
		t.Fatalf("ResponseController.Flush() = %v, want nil", err)
	}
}

// realFlusher is a ResponseWriter that is genuinely flushable, used to prove
// Flush() actually reaches the wrapped writer rather than being swallowed.
type realFlusher struct {
	header  http.Header
	flushed bool
}

func (f *realFlusher) Header() http.Header         { return f.header }
func (f *realFlusher) Write(b []byte) (int, error) { return len(b), nil }
func (f *realFlusher) WriteHeader(int)             {}
func (f *realFlusher) Flush()                      { f.flushed = true }

func TestFlushReachesUnderlyingWriter(t *testing.T) {
	inner := &realFlusher{header: http.Header{}}
	rec := &statusRecorder{ResponseWriter: inner}
	rec.Flush()
	if !inner.flushed {
		t.Fatal("Flush did not propagate to the underlying writer")
	}
}

func TestLoggingDoesNotWriteHeadersTwice(t *testing.T) {
	inner := &realFlusher{header: http.Header{}}
	rec := &statusRecorder{ResponseWriter: inner}
	rec.WriteHeader(http.StatusTeapot)
	rec.WriteHeader(http.StatusOK)
	if rec.status != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", rec.status)
	}
}

// --- helpers ---

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
