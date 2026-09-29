package middleware

import (
	"context"
	"encoding/json"
	"net/http"
)

// FeatureGate is the contract middleware needs to evaluate a feature flag.
//
// It is declared here, at the consumer, rather than importing
// service.FeatureFlagService. That is the whole point: the middleware package
// previously reached into internal/service and internal/repository to build the
// token verifier, and the feature-flag middleware did not exist in this package
// at all -- it was a method on a handler, so the router had to reach through a
// handler constructor to install a cross-cutting policy. A one-method interface
// is enough to invert the dependency without a new import.
type FeatureGate interface {
	Enabled(ctx context.Context, key string) (bool, error)
}

// RequireFeature rejects requests when a flag is disabled.
//
// An empty key is a pass-through, which is how a route whose flag may be unset
// is registered: flagMw("") always lets the request through.
//
// A lookup failure is reported to the client as FEATURE_DISABLED, matching the
// behaviour this had when it lived on the handler. For a kill switch that is
// the right default -- a system that cannot confirm a feature is enabled should
// not serve it -- and the service now logs and counts the failure so it is
// distinguishable from a genuine "off". The previous version swallowed the
// error entirely, so a database outage and an operator's decision were
// indistinguishable from the outside and from the logs.
func RequireFeature(gate FeatureGate, key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if key == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			enabled, err := gate.Enabled(r.Context(), key)
			if err != nil || !enabled {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    "FEATURE_DISABLED",
						"message": "fitur ini sedang nonaktif",
						"flag":    key,
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
