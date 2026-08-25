package cache

import (
	"context"
	"encoding/json"
	"math/rand"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store is a typed JSON cache over Redis.
type Store struct {
	rdb *redis.Client
}

// NewStore creates a cache Store.
func NewStore(rdb *redis.Client) *Store {
	return &Store{rdb: rdb}
}

// Get decodes a cached value; returns false on miss.
func (s *Store) Get(ctx context.Context, key string, dest any) (bool, error) {
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, err
	}
	return true, nil
}

// Set encodes and stores a value with a TTL.
func (s *Store) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, key, raw, ttl).Err()
}

// SetJitter stores a value with the base TTL plus 0-20% jitter so popular
// keys don't all expire simultaneously (cache-stampede guard).
func (s *Store) SetJitter(ctx context.Context, key string, value any, base time.Duration) error {
	jitter := time.Duration(rand.Int63n(int64(base)/5 + 1))
	return s.Set(ctx, key, value, base+jitter)
}

// Del removes a key.
func (s *Store) Del(ctx context.Context, keys ...string) error {
	return s.rdb.Del(ctx, keys...).Err()
}

// Incr bumps a counter key.
func (s *Store) Incr(ctx context.Context, key string) error {
	return s.rdb.Incr(ctx, key).Err()
}

// GetInt reads a counter key (0 when absent).
func (s *Store) GetInt(ctx context.Context, key string) int64 {
	v, err := s.rdb.Get(ctx, key).Int64()
	if err != nil {
		return 0
	}
	return v
}
