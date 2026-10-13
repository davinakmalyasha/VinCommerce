package middleware

import (
	"os"
	"strings"
	"testing"
	"time"
)

// THE DEFECT.
//
// Both Redis error paths in `Limit` were:
//
//	if err != nil {
//		next.ServeHTTP(w, r)
//		return
//	}
//
// Fail OPEN, and silently. A Redis blip returned /auth/login to unlimited
// attempts -- the one route where the limit is a security control rather than a
// politeness measure -- and left no trace, so an outage and a quiet day looked
// identical in the logs.
//
// The fallback keeps limiting in-process and says so. These tests pin both halves:
// the local limiter actually limits, and the Redis failure path is neither silent
// nor a bypass.
func TestTheRedisFailurePathKeepsLimitingAndSaysSo(t *testing.T) {
	src := ratelimitSource(t)

	at := strings.Index(src, "allowed, remaining, err := l.allowRedis(")
	if at < 0 {
		t.Fatal("the Redis call is no longer an error-returning helper; update this test")
	}
	end := strings.Index(src[at:], "w.Header().Set(\"X-RateLimit-Limit\"")
	if end < 0 {
		t.Fatal("could not bound the fallback block")
	}
	block := src[at : at+end]

	if !strings.Contains(block, "l.local.allow(") {
		t.Errorf("the Redis failure path does not consult the in-process limiter.\n%s\n"+
			"That is fail-open: a Redis outage makes every guarded route unlimited", block)
	}
	if !strings.Contains(block, "noteFallback(") {
		t.Errorf("the Redis failure path does not report the fallback.\n%s\n"+
			"A limiter that stopped limiting without saying so is indistinguishable "+
			"from one that is working -- which is the defect being fixed", block)
	}
	// The old shape: serve and return on error, with no limiter involved.
	if strings.Contains(block, "next.ServeHTTP(w, r)\n\t\t\t\treturn") {
		t.Errorf("the failure path still serves unconditionally.\n%s", block)
	}
}

func TestLocalLimiterAllowsUpToMaxThenRefuses(t *testing.T) {
	l := newLocalLimiter()
	now := time.Now()
	const max = 3
	const window = time.Minute

	for i := 1; i <= max; i++ {
		if allowed, _ := l.allow("user-1", max, window, now); !allowed {
			t.Fatalf("request %d of %d was refused", i, max)
		}
	}
	if allowed, remaining := l.allow("user-1", max, window, now); allowed {
		t.Error("the request past the limit was allowed; the fallback does not limit")
	} else if remaining != 0 {
		t.Errorf("remaining = %d when over the limit, want 0", remaining)
	}

	// Identifiers are independent, or one busy user would lock out everyone.
	if allowed, _ := l.allow("user-2", max, window, now); !allowed {
		t.Error("a different identifier was refused; the fallback is keying globally")
	}
}

// A window that never expires would rate-limit an identity for the life of the
// process -- the exact bug the Redis SET NX + TTL dance above this exists to avoid.
func TestLocalLimiterWindowExpires(t *testing.T) {
	l := newLocalLimiter()
	now := time.Now()
	const max = 1
	const window = time.Minute

	if allowed, _ := l.allow("user-1", max, window, now); !allowed {
		t.Fatal("the first request was refused")
	}
	if allowed, _ := l.allow("user-1", max, window, now); allowed {
		t.Fatal("the second request inside the window was allowed")
	}
	if allowed, _ := l.allow("user-1", max, window, now.Add(window+time.Second)); !allowed {
		t.Error("a request after the window expired was still refused")
	}
}

// The map is keyed by an attacker-influenced identifier, so an unbounded one would
// be a memory-exhaustion vector -- during a Redis outage, which is when an
// attacker would choose. Verify it refuses rather than growing past the bound.
func TestLocalLimiterRefusesRatherThanGrowingUnbounded(t *testing.T) {
	l := newLocalLimiter()
	now := time.Now()

	for i := 0; i < maxLocalWindows; i++ {
		l.allow(itoa(i), 5, time.Minute, now)
	}
	if len(l.windows) > maxLocalWindows {
		t.Fatalf("the map grew to %d, past the bound of %d", len(l.windows), maxLocalWindows)
	}
	if allowed, _ := l.allow("one-more", 5, time.Minute, now); allowed {
		t.Error("a new identifier was admitted past the bound; the map can grow without limit")
	}

	// Once the windows expire, the map drains and service resumes.
	if allowed, _ := l.allow("one-more", 5, time.Minute, now.Add(2*time.Minute)); !allowed {
		t.Error("the limiter stayed full after its windows expired, so it would never recover")
	}
}

func ratelimitSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("ratelimit.go")
	if err != nil {
		t.Fatalf("read ratelimit.go: %v", err)
	}
	return string(raw)
}
