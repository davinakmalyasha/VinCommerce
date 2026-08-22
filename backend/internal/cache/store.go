package cache

import (
	"context"
	"encoding/json"
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

// Del removes a key.
func (s *Store) Del(ctx context.Context, keys ...string) error {
	return s.rdb.Del(ctx, keys...).Err()
}
