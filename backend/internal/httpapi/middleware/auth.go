package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/service"
)

type userKey struct{}

// WithUser stores the authenticated user in the context.
func WithUser(ctx context.Context, u *domain.User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// UserFrom returns the authenticated user, or nil.
func UserFrom(ctx context.Context) *domain.User {
	u, _ := ctx.Value(userKey{}).(*domain.User)
	return u
}

// VerChecker reports whether a token's ver claim is still current.
type VerChecker func(userID string, ver int) bool

// CachedVerChecker compares against users.token_ver with a short TTL cache,
// so role/status changes propagate within ~ttl instead of waiting out the
// full access-token lifetime — while keeping steady-state auth DB-free.
func CachedVerChecker(users *repository.UserRepository, ttl time.Duration) VerChecker {
	type entry struct {
		ver int
		at  time.Time
	}
	var mu sync.Mutex
	cache := map[string]entry{}
	return func(userID string, ver int) bool {
		if userID == "" {
			return true
		}
		now := time.Now()
		mu.Lock()
		e, ok := cache[userID]
		mu.Unlock()
		if !ok || now.Sub(e.at) >= ttl {
			current := users.TokenVersion(context.Background(), userID)
			e = entry{ver: current, at: now}
			mu.Lock()
			cache[userID] = e
			mu.Unlock()
		}
		return e.ver == ver
	}
}

// Authenticate validates the Bearer access token.
// verCheck may be nil (skips token-version validation).
func Authenticate(tokens *service.TokenManager, verCheck VerChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			tokenStr, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || tokenStr == "" {
				http.Error(w, `{"error":{"code":"UNAUTHENTICATED","message":"missing bearer token"}}`, http.StatusUnauthorized)
				return
			}

			user, err := tokens.ParseAccess(tokenStr)
			if err != nil {
				http.Error(w, `{"error":{"code":"TOKEN_INVALID","message":"invalid or expired token"}}`, http.StatusUnauthorized)
				return
			}
			if verCheck != nil && !verCheck(user.ID, user.TokenVer) {
				http.Error(w, `{"error":{"code":"TOKEN_STALE","message":"session was revoked or changed, please refresh"}}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
		})
	}
}

// AuthenticateOptional resolves the user when a valid token is present, otherwise continues as guest.
func AuthenticateOptional(tokens *service.TokenManager, verCheck VerChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			tokenStr, ok := strings.CutPrefix(header, "Bearer ")
			if ok && tokenStr != "" {
				if user, err := tokens.ParseAccess(tokenStr); err == nil {
					if verCheck == nil || verCheck(user.ID, user.TokenVer) {
						next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRoles restricts access to users holding at least one role.
func RequireRoles(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := UserFrom(r.Context())
			if user == nil {
				http.Error(w, `{"error":{"code":"UNAUTHENTICATED","message":"authentication required"}}`, http.StatusUnauthorized)
				return
			}
			for _, role := range roles {
				if user.HasRole(role) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, `{"error":{"code":"FORBIDDEN","message":"insufficient permissions"}}`, http.StatusForbidden)
		})
	}
}
