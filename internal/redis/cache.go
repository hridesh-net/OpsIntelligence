package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Cache is a generic Redis-backed cache with JSON serialization.
// It gracefully falls back to a no-op when Redis is disabled.
type Cache struct {
	client *Client
	log    *zap.Logger
}

// NewCache returns a Cache backed by the given Redis client.
// If client is nil/disabled, all Cache methods become no-ops.
func NewCache(client *Client, log *zap.Logger) *Cache {
	if log == nil {
		log = zap.NewNop()
	}
	return &Cache{client: client, log: log}
}

// Enabled reports whether the cache is functional.
func (c *Cache) Enabled() bool { return c.client != nil && c.client.Enabled() }

// Set stores a value in Redis with the default TTL.
func (c *Cache) Set(ctx context.Context, key string, value any) error {
	return c.SetWithTTL(ctx, key, value, c.client.Config().CacheTTL)
}

// SetWithTTL stores a value with an explicit TTL.
func (c *Cache) SetWithTTL(ctx context.Context, key string, value any, ttl time.Duration) error {
	if !c.Enabled() {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("redis cache: marshal: %w", err)
	}
	prefixed := c.client.Key("cache", key)
	if err := c.client.Client().Set(ctx, prefixed, data, ttl).Err(); err != nil {
		c.log.Warn("redis cache set failed", zap.String("key", key), zap.Error(err))
		return err
	}
	return nil
}

// Get retrieves a value from Redis and unmarshals it into dest.
// Returns goredis.Nil when the key is absent.
func (c *Cache) Get(ctx context.Context, key string, dest any) error {
	if !c.Enabled() {
		return goredis.Nil
	}
	prefixed := c.client.Key("cache", key)
	data, err := c.client.Client().Get(ctx, prefixed).Result()
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(data), dest); err != nil {
		c.log.Warn("redis cache get unmarshal failed", zap.String("key", key), zap.Error(err))
		return fmt.Errorf("redis cache: unmarshal: %w", err)
	}
	return nil
}

// Delete removes a key from the cache.
func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	if !c.Enabled() || len(keys) == 0 {
		return nil
	}
	prefixed := make([]string, len(keys))
	for i, k := range keys {
		prefixed[i] = c.client.Key("cache", k)
	}
	if err := c.client.Client().Del(ctx, prefixed...).Err(); err != nil {
		c.log.Warn("redis cache delete failed", zap.Strings("keys", keys), zap.Error(err))
		return err
	}
	return nil
}

// GetOrSet is a convenience helper that checks the cache, calls fetch() on miss,
// stores the result, and returns it. fetch() is only called when the key is absent.
func (c *Cache) GetOrSet(ctx context.Context, key string, dest any, ttl time.Duration, fetch func() (any, error)) error {
	if err := c.Get(ctx, key, dest); err == nil {
		return nil
	} else if err != goredis.Nil {
		c.log.Debug("redis cache get error, falling back to fetch", zap.String("key", key), zap.Error(err))
	}

	val, err := fetch()
	if err != nil {
		return err
	}
	if err := c.SetWithTTL(ctx, key, val, ttl); err != nil {
		c.log.Warn("redis cache set failed after fetch", zap.String("key", key), zap.Error(err))
	}
	// Unmarshal the fetched value into dest.
	data, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("redis cache: marshal fetched value: %w", err)
	}
	return json.Unmarshal(data, dest)
}
