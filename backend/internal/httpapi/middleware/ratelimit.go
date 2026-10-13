package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// fallbackWarnEvery bounds how often the Redis outage is logged.
//
// One line per window, not per request: an outage makes every request take this
// path, and a log line each would turn a Redis blip into a log flood precisely
// when the log is the thing being read.
const fallbackWarnEvery = time.Minute

// maxLocalWindows bounds the in-process fallback's map.
//
// The fallback keys by the same identifier as Redis -- user id, IP, session --
// which is attacker-influenced. Without a bound, a flood of distinct identifiers
// during a Redis outage would grow the map until the process died, turning a
// degradation into an outage of a different kind.
const maxLocalWindows = 100_000

// RateLimiter implements fixed-window rate limiting backed by Redis.
//
// Redis is the source of truth because the window has to be shared across
// replicas: per-process limits multiply by the replica count, and the login
// limiter exists to bound credential stuffing rather than to shape traffic.
type RateLimiter struct {
	rdb *redis.Client

	// local is used when Redis cannot answer. See noteFallback.
	local    *localLimiter
	mu       sync.Mutex
	lastWarn time.Time
}

// NewRateLimiter creates a RateLimiter.
func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{rdb: rdb, local: newLocalLimiter()}
}

// Limit enforces `max` requests per `window` for an identifier extracted from the request.
// Uses SET NX to seed the window atomically, so a crash between INCR and EXPIRE
// can never leave a permanent key that rate-limits an identity forever.
//
// WHEN REDIS IS DOWN THIS KEEPS LIMITING, AND SAYS SO.
//
// The Redis error paths used to `next.ServeHTTP` and return: fail OPEN, and
// silently. A Redis blip returned /auth/login to unlimited attempts -- the one
// route where the limit is a security control rather than a politeness measure --
// and left no record that it had happened, so an outage and a quiet day looked
// identical from the logs.
//
// Failing CLOSED was the other option and was rejected: refusing every login
// during a Redis outage converts a degradation into a total lockout. A
// per-process limiter is weaker than a shared one -- N replicas admit N x max --
// but it is bounded, it is strictly better than nothing, and it is logged.
func (l *RateLimiter) Limit(max int, window time.Duration, key func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := key(r)

			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()

			allowed, remaining, err := l.allowRedis(ctx, "rl:"+id, max, window)
			if err != nil {
				// Never silently: a limiter that stopped limiting without saying so
				// is indistinguishable from one that is working.
				l.noteFallback(err)
				allowed, remaining = l.local.allow(id, max, window, time.Now())
			}

			w.Header().Set("X-RateLimit-Limit", itoa(max))
			w.Header().Set("X-RateLimit-Remaining", itoa(remaining))

			if !allowed {
				w.Header().Set("Retry-After", itoa(int(window.Seconds())))
				http.Error(w, `{"error":{"code":"RATE_LIMITED","message":"too many requests"}}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// allowRedis runs the shared fixed window. A non-nil error is the caller's signal
// to fall back; it is never swallowed here.
func (l *RateLimiter) allowRedis(ctx context.Context, redisKey string, max int, window time.Duration) (bool, int, error) {
	seeded, err := l.rdb.SetNX(ctx, redisKey, 0, window).Result()
	if err != nil {
		return false, 0, err
	}
	count, err := l.rdb.Incr(ctx, redisKey).Result()
	if err != nil {
		return false, 0, err
	}
	if seeded && count == 1 {
		l.rdb.Expire(ctx, redisKey, window) // belt & braces; NX already set TTL
	}
	return count <= int64(max), remainingAfter(max, int(count)), nil
}

// remainingAfter clamps the advertised remaining count at zero.
//
// It used to be `max - count`, which goes NEGATIVE once the window is exhausted
// because the counter keeps rising. A negative `X-RateLimit-Remaining` is not a
// number any client can act on, and it was emitted on every over-limit response.
func remainingAfter(max, count int) int {
	if count >= max {
		return 0
	}
	return max - count
}

// noteFallback logs that limiting has moved in-process. Rate-limited to one line
// per fallbackWarnEvery so a sustained outage cannot flood the log.
func (l *RateLimiter) noteFallback(err error) {
	l.mu.Lock()
	now := time.Now()
	if now.Sub(l.lastWarn) < fallbackWarnEvery {
		l.mu.Unlock()
		return
	}
	l.lastWarn = now
	l.mu.Unlock()

	slog.Warn("rate limiter falling back to the in-process limiter; Redis is unavailable",
		"error", err.Error())
}

// localLimiter is the in-process fallback: a fixed window per identifier, held in
// memory, used only while Redis cannot answer.
type localLimiter struct {
	mu      sync.Mutex
	windows map[string]*localWindow
}

type localWindow struct {
	count   int
	expires time.Time
}

func newLocalLimiter() *localLimiter {
	return &localLimiter{windows: make(map[string]*localWindow)}
}

// allow reports whether the request is within the window, incrementing the count.
func (l *localLimiter) allow(id string, max int, window time.Duration, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Evict in a batch rather than per request: a sweep on every call would be
	// O(distinct identifiers) per request, which is the same unbounded work the
	// bound exists to prevent. Sweeping when the map is large or a window has
	// passed keeps it amortised.
	if len(l.windows) >= maxLocalWindows {
		for k, w := range l.windows {
			if now.After(w.expires) {
				delete(l.windows, k)
			}
		}
		// Still full after eviction: every window in there is LIVE, which means
		// maxLocalWindows distinct identifiers arrived inside one window. That is
		// itself the signal, so the answer is to refuse rather than to admit
		// unbounded growth -- admitting is what turns the fallback into the outage
		// the bound exists to prevent.
		if len(l.windows) >= maxLocalWindows {
			return false, 0
		}
	}

	w, ok := l.windows[id]
	if !ok || now.After(w.expires) {
		w = &localWindow{expires: now.Add(window)}
		l.windows[id] = w
	}
	w.count++
	return w.count <= max, remainingAfter(max, w.count)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
