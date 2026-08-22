package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/vincommerce/backend/internal/domain"
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

// Authenticate validates the Bearer access token.
func Authenticate(tokens *service.TokenManager) func(http.Handler) http.Handler {
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
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
		})
	}
}

// AuthenticateOptional resolves the user when a valid token is present, otherwise continues as guest.
func AuthenticateOptional(tokens *service.TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			tokenStr, ok := strings.CutPrefix(header, "Bearer ")
			if ok && tokenStr != "" {
				if user, err := tokens.ParseAccess(tokenStr); err == nil {
					next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
					return
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
