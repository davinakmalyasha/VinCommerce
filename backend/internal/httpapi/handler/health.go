package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
)

var startTime = time.Now()

// Version is the deployed build identifier, injected via
// -ldflags "-X github.com/vincommerce/backend/internal/httpapi/handler.Version=<sha>".
var Version = "dev"

// Health holds dependencies for the health endpoints.
type Health struct {
	pool  *db.Pool
	redis *redis.Client
	cfg   *config.Config
}

// NewHealth creates a Health handler.
func NewHealth(pool *db.Pool, rdb *redis.Client, cfg *config.Config) *Health {
	return &Health{pool: pool, redis: rdb, cfg: cfg}
}

type healthResponse struct {
	Status      string    `json:"status"`
	Environment string    `json:"environment"`
	Version     string    `json:"version"`
	Uptime      string    `json:"uptime"`
	Time        time.Time `json:"time"`
	Checks      struct {
		Database string `json:"database"`
		Redis    string `json:"redis"`
	} `json:"checks"`
	RequestID string `json:"request_id,omitempty"`
}

// Liveness responds regardless of dependencies.
func (h *Health) Liveness(w http.ResponseWriter, r *http.Request) {
	h.write(w, r, http.StatusOK, "ok")
}

// Readiness verifies database connectivity.
func (h *Health) Readiness(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:      "ok",
		Environment: h.cfg.Environment,
		Version:     Version,
		Uptime:      time.Since(startTime).Round(time.Second).String(),
		Time:        time.Now().UTC(),
		RequestID:   middleware.RequestIDFrom(r.Context()),
	}
	resp.Checks.Database = "ok"
	resp.Checks.Redis = "ok"

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.pool.Ping(ctx); err != nil {
		resp.Status = "degraded"
		resp.Checks.Database = "unreachable"
	}
	if err := h.redis.Ping(ctx).Err(); err != nil {
		resp.Status = "degraded"
		resp.Checks.Redis = "unreachable"
	}

	code := http.StatusOK
	if resp.Status != "ok" {
		code = http.StatusServiceUnavailable
	}
	h.write(w, r, code, resp)
}

func (h *Health) write(w http.ResponseWriter, r *http.Request, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
