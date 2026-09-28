package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// auditColumnLimit mirrors the widest audit_log column the middleware writes
// to. Exceeding it is not a truncation, it is a rejected INSERT.
const (
	maxAuditActionLen = 64 // audit_log.action        VARCHAR(64) NOT NULL
	maxAuditEntityLen = 64 // audit_log.entity_id     VARCHAR(64)
	maxAuditAgentLen  = 512
)

// truncate shortens s to at most n runes, appending an ellipsis marker so a
// truncated value is never mistaken for a complete one.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

// safeInet returns v if it parses as an IP, else "".
//
// The audit repository writes ip_address with NULLIF($5,”)::inet. A
// non-empty but unparseable value — which is exactly what a spoofed
// X-Forwarded-For produces — made Postgres raise `invalid input syntax for
// type inet` and fail the WHOLE INSERT.
func safeInet(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// Strip a port if one slipped through; ClientIP does not return one, but
	// a future caller might.
	if host, _, err := net.SplitHostPort(v); err == nil {
		v = host
	}
	if ip := net.ParseIP(v); ip != nil {
		return ip.String()
	}
	return ""
}

// AuditMiddleware records authenticated mutating requests to the audit log.
//
// This is the evidence trail for refunds, impersonation and role grants, so it
// must be impossible for a request to destroy its own record. Three
// attacker-controlled inputs previously could:
//
//   - an X-Forwarded-For that does not parse as an IP -> `::inet` cast error
//   - an oversized X-Request-ID -> `value too long` on entity_id
//   - a request path longer than ~58 chars -> `value too long` on action
//
// In every case the INSERT failed and the error was discarded, so
// auth.login, every admin mutation, and admin.impersonate could silently
// vanish from the record.
//
// The action is also normalised to a discrete verb plus the entity, so the
// audit viewer can filter on it; the full path goes to metadata, which is
// unbounded JSONB.
func AuditMiddleware(sessions *repository.SessionRepository, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
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

			clientIP := ClientIP(r)
			ip := safeInet(clientIP)
			if ip == "" && clientIP != "" {
				// The address is unusable as INET; record it as text in the
				// metadata so the information is not lost, but do NOT put it
				// in the inet column.
				metadata["client_addr_unparsed"] = truncate(clientIP, 128)
			}
			if ua := truncate(r.UserAgent(), maxAuditAgentLen); ua != "" {
				metadata["user_agent"] = ua
			}
			metadata["path"] = r.URL.Path
			if r.URL.RawQuery != "" {
				metadata["query"] = truncate(r.URL.RawQuery, 512)
			}
			// Keep the client-supplied correlation ID separate from the entity
			// identifier, which is now server-generated.
			if client := r.Header.Get("X-Request-ID"); client != "" {
				metadata["client_request_id"] = truncate(client, maxAuditEntityLen)
			}

			action := auditAction(r)
			entityID := RequestIDFrom(r.Context())

			if err := sessions.Audit(r.Context(), &domain.AuditEntry{
				ActorID:    actorID,
				Action:     action,
				EntityType: "http",
				EntityID:   truncate(entityID, maxAuditEntityLen),
				IPAddress:  ip,
				UserAgent:  truncate(r.UserAgent(), maxAuditAgentLen),
				Metadata:   metadata,
			}); err != nil {
				// Never swallow this. A silently missing audit row is how
				// audit-trail evasion goes unnoticed during an incident.
				logger.Error("audit write failed",
					"action", action,
					"actor_id", actorID,
					"request_id", entityID,
					"error", err.Error(),
				)
			}
		})
	}
}

// auditAction builds a discrete, filterable action string that always fits the
// 64-character column. The previous "POST /api/v1/payments/sandbox/orders/
// <uuid>/approve" form overflowed for any path over ~58 characters, which made
// those routes permanently unauditable.
func auditAction(r *http.Request) string {
	verb := map[string]string{
		http.MethodPost:   "POST",
		http.MethodPut:    "PUT",
		http.MethodPatch:  "PATCH",
		http.MethodDelete: "DELETE",
	}[r.Method]

	// Reduce the path to its meaningful segments: the API prefix and any
	// path parameter placeholders are dropped so the verb+resource form is
	// short and stable across requests.
	segments := []string{}
	for _, seg := range strings.Split(strings.Trim(r.URL.Path, "/"), "/") {
		if seg == "" || seg == "api" || seg == "v1" {
			continue
		}
		// Drop path parameters: uuids, numeric ids and slugs.
		if isPathParam(seg) {
			segments = append(segments, ":id")
			continue
		}
		segments = append(segments, seg)
	}

	action := verb + " " + strings.Join(segments, "/")
	return truncate(action, maxAuditActionLen)
}

// isPathParam reports whether a path segment looks like an identifier rather
// than a resource name.
func isPathParam(seg string) bool {
	if net.ParseIP(seg) != nil {
		return true
	}
	// A uuid: 8-4-4-4-12 hex.
	if len(seg) == 36 && strings.Count(seg, "-") == 4 {
		return true
	}
	// Any all-digit segment.
	allDigits := true
	for _, r := range seg {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	return allDigits && len(seg) > 0
}
