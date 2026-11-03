package service

import (
	"strings"
	"testing"
)

// The live viewer count only grew: Stream.Live joined without ever leaving,
// and the peak added one per join instead of tracking concurrent viewers.
// These assert the pairing and the formula, which no behavioural test can
// reach without a live database and an SSE client.
func TestViewerJoinIsPairedWithLeave(t *testing.T) {
	src := readFileAt(t, "../httpapi/handler/stream.go")
	join := strings.Index(src, "h.live.ViewerJoin(r.Context(), sessionID)")
	if join < 0 {
		t.Fatal("Stream.Live no longer records viewer joins")
	}
	window := src[join : join+300]
	if !strings.Contains(window, "defer h.live.ViewerLeave(sessionID)") {
		t.Errorf("viewer join is not paired with a deferred leave:\n%s\n"+
			"every SSE disconnect must decrement, or viewer_count leaks upward", window)
	}
}

func TestViewerLeaveSurvivesACancelledContext(t *testing.T) {
	src := readSource(t, "live_service.go")
	i := strings.Index(src, "func (s *LiveService) ViewerLeave(")
	if i < 0 {
		t.Fatal("no ViewerLeave on LiveService")
	}
	fn := src[i:]
	if end := strings.Index(fn, "\n}\n"); end > 0 {
		fn = fn[:end]
	}
	// The request context is done when leave runs; executing on it fails.
	if !strings.Contains(fn, "context.Background()") {
		t.Errorf("ViewerLeave does not use a fresh context:\n%s\n"+
			"running the decrement on the cancelled request context fails silently", fn)
	}
}

func TestViewerPeakIsAHighWaterMark(t *testing.T) {
	src := readRepositorySource(t, "live_repo.go")
	i := strings.Index(src, "func (r *LiveRepository) BumpViewer(")
	if i < 0 {
		t.Fatal("no BumpViewer in live_repo.go")
	}
	fn := src[i:]
	if end := strings.Index(fn, "\n}\n"); end > 0 {
		fn = fn[:end]
	}
	// viewer_peak + 1 on every join makes the "peak" count total joins.
	if strings.Contains(fn, "viewer_peak + 1") {
		t.Errorf("peak accumulates joins instead of tracking concurrency:\n%s", fn)
	}
	if !strings.Contains(fn, "GREATEST(viewer_peak, viewer_count + 1)") {
		t.Errorf("peak is not max(peak, live count):\n%s", fn)
	}
}
