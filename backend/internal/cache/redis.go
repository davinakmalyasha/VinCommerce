package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/vincommerce/backend/internal/config"
)

// Client wraps go-redis.
type Client struct {
	*redis.Client
}

// Connect creates a Redis client and verifies connectivity.
func Connect(ctx context.Context, cfg config.RedisConfig) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return nil, err
	}
	return &Client{Client: rdb}, nil
}
