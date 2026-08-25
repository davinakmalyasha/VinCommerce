package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimiter implements fixed-window rate limiting backed by Redis.
type RateLimiter struct {
	rdb *redis.Client
}

// NewRateLimiter creates a RateLimiter.
func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{rdb: rdb}
}

// Limit enforces `max` requests per `window` for an identifier extracted from the request.
// Uses SET NX to seed the window atomically, so a crash between INCR and EXPIRE
// can never leave a permanent key that rate-limits an identity forever.
func (l *RateLimiter) Limit(max int, window time.Duration, key func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := key(r)
			redisKey := "rl:" + id

			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()

			seeded, err := l.rdb.SetNX(ctx, redisKey, 0, window).Result()
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			count, err := l.rdb.Incr(ctx, redisKey).Result()
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if seeded && count == 1 {
				l.rdb.Expire(ctx, redisKey, window) // belt & braces; NX already set TTL
			}
			w.Header().Set("X-RateLimit-Limit", itoa(max))
			w.Header().Set("X-RateLimit-Remaining", itoa(max-int(count)))

			if count > int64(max) {
				w.Header().Set("Retry-After", itoa(int(window.Seconds())))
				http.Error(w, `{"error":{"code":"RATE_LIMITED","message":"too many requests"}}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
