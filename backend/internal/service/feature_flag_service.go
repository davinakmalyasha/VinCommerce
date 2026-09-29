package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/vincommerce/backend/internal/repository"
)

// FeatureFlagStore is the persistence slice a flag service needs. Declared at
// the consumer rather than depending on a concrete repository type -- the whole
// point of the extraction, since the handler used to hold
// *repository.FeatureFlagRepository directly.
type FeatureFlagStore interface {
	List(ctx context.Context) ([]*repository.FeatureFlag, error)
	IsEnabled(ctx context.Context, key string) (bool, error)
	SetEnabled(ctx context.Context, key string, enabled bool) error
}

// FeatureFlagTTL is how long a flag evaluation is cached in process.
//
// The previous implementation queried the database on every single request to a
// flagged route -- /ai/ask is limited to 20/min, but /live/{id}/chat and
// /games/* are not rate limited at all. Flags change when an admin clicks a
// toggle, which is measured in minutes at most, so a 30-second cache removes
// effectively all of that load at the cost of up to 30 seconds of propagation
// delay after a toggle.
//
// The 30s is deliberate rather than longer: a kill-switch that takes a minute to
// take effect is not a kill-switch.
const FeatureFlagTTL = 30 * time.Second

// FeatureFlagService owns feature-flag evaluation and the admin surface.
//
// It exists because this used to live in httpapi/handler/admin_ops.go, which is
// where two separate architectural defects sat:
//
//   - the handler held a concrete *FeatureFlagRepository, inverting the
//     dependency direction, and
//   - the handler DEFINED the middleware that gates every flagged route. A
//     cross-cutting kill-switch policy living in a transport type is a policy
//     with no home: nothing else can consult a flag, nothing can test it, and
//     the router has to reach through a handler constructor to get it.
type FeatureFlagService struct {
	store  FeatureFlagStore
	logger *slog.Logger

	mu      sync.RWMutex
	entries map[string]flagEntry
}

type flagEntry struct {
	enabled   bool
	fetchedAt time.Time
}

// NewFeatureFlagService constructs the service.
func NewFeatureFlagService(store FeatureFlagStore, logger *slog.Logger) *FeatureFlagService {
	if logger == nil {
		logger = slog.Default()
	}
	return &FeatureFlagService{
		store:   store,
		logger:  logger,
		entries: map[string]flagEntry{},
	}
}

// Enabled reports whether a flag is on. An empty key is a pass-through, which
// is how the router registers a route whose flag may be unset.
//
// A storage error is reported as DISABLED, which is the previous behaviour and
// the correct one for this particular decision: the flag exists to hold a
// feature back, and a system that cannot confirm the flag is on should not
// serve the feature. The difference is that it is now logged and counted rather
// than being indistinguishable from a real "off", so a database blip is
// diagnosable instead of looking like an operator's decision.
func (s *FeatureFlagService) Enabled(ctx context.Context, key string) (bool, error) {
	if key == "" {
		return true, nil
	}
	s.mu.RLock()
	e, ok := s.entries[key]
	s.mu.RUnlock()
	if ok && time.Since(e.fetchedAt) < FeatureFlagTTL {
		return e.enabled, nil
	}

	enabled, err := s.store.IsEnabled(ctx, key)
	if err != nil {
		s.logger.Error("feature flag lookup failed; treating the feature as disabled",
			"flag", key, "error", err.Error(),
			"note", "if this recurs the database is unreachable, not the flag")
		// Negative-cache briefly so a sustained outage does not turn into a
		// query per request.
		s.remember(key, false)
		return false, err
	}
	s.remember(key, enabled)
	return enabled, nil
}

func (s *FeatureFlagService) remember(key string, enabled bool) {
	s.mu.Lock()
	s.entries[key] = flagEntry{enabled: enabled, fetchedAt: time.Now()}
	s.mu.Unlock()
}

// List returns all flags for the admin console.
func (s *FeatureFlagService) List(ctx context.Context) ([]*repository.FeatureFlag, error) {
	items, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	// Refresh the cache from the authoritative list so a toggle in another
	// replica does not take up to 30s to be visible here when we happen to be
	// listing.
	s.mu.Lock()
	for _, f := range items {
		if f == nil {
			continue
		}
		s.entries[f.Key] = flagEntry{enabled: f.Enabled, fetchedAt: time.Now()}
	}
	s.mu.Unlock()
	return items, nil
}

// SetEnabled toggles a flag and updates the local cache immediately, so the
// admin who flipped it sees the change on their next request rather than after
// the TTL.
func (s *FeatureFlagService) SetEnabled(ctx context.Context, key string, enabled bool) error {
	if err := s.store.SetEnabled(ctx, key, enabled); err != nil {
		return err
	}
	s.remember(key, enabled)
	s.logger.Info("feature flag changed", "flag", key, "enabled", enabled)
	return nil
}

// Invalidate drops a cached flag, forcing the next Enabled call to re-read.
// Used by tests and by any future pub/sub-based flag propagation.
func (s *FeatureFlagService) Invalidate() {
	s.mu.Lock()
	s.entries = map[string]flagEntry{}
	s.mu.Unlock()
}
