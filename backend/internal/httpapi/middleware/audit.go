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
			// Impersonation attribution: the token subject is the *target*
			// user, but the real actor is the admin in the act_as claim.
			// Record the admin as the actor and the target as context.
			actorID := user.ID
			metadata := map[string]any{}
			if user.ActingAs != nil && *user.ActingAs != "" {
				actorID = *user.ActingAs
				metadata["acting_as"] = user.ID
			}
			_ = sessions.Audit(r.Context(), &domain.AuditEntry{
				ActorID:    actorID,
				Action:     r.Method + " " + r.URL.Path,
				EntityType: "http",
				EntityID:   RequestIDFrom(r.Context()),
				IPAddress:  r.RemoteAddr,
				UserAgent:  r.UserAgent(),
				Metadata:   metadata,
			})
		})
	}
}
