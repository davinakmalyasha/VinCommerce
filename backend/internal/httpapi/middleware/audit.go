package middleware

import (
	"net/http"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// AuditMiddleware records authenticated mutating requests to the audit log.
func AuditMiddleware(sessions *repository.SessionRepository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			if rec.status >= 400 {
				return
			}
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				return
			}

			user := UserFrom(r.Context())
			if user == nil {
				return
			}
			_ = sessions.Audit(r.Context(), &domain.AuditEntry{
				ActorID:    user.ID,
				Action:     r.Method + " " + r.URL.Path,
				EntityType: "http",
				EntityID:   RequestIDFrom(r.Context()),
				IPAddress:  r.RemoteAddr,
				UserAgent:  r.UserAgent(),
			})
		})
	}
}
