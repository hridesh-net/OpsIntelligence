package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// SessionCache wraps a datastore.SessionRepo with a Redis read cache.
// It dramatically reduces database load on the hot path (every authenticated
// HTTP request calls Get() at least once).
type SessionCache struct {
	inner  datastore.SessionRepo
	cache  *Cache
	ttl    time.Duration
	log    *zap.Logger
}

// NewSessionCache returns a caching decorator. If cache is nil/disabled,
// all operations pass through to inner with no overhead.
func NewSessionCache(inner datastore.SessionRepo, cache *Cache, ttl time.Duration, log *zap.Logger) datastore.SessionRepo {
	if cache == nil || !cache.Enabled() {
		return inner
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SessionCache{
		inner: inner,
		cache: cache,
		ttl:   ttl,
		log:   log,
	}
}

func (c *SessionCache) cacheKey(id string) string {
	return "session:" + id
}

func (c *SessionCache) Create(ctx context.Context, s *datastore.Session) error {
	if err := c.inner.Create(ctx, s); err != nil {
		return err
	}
	// Warm the cache immediately so the next read hits Redis.
	_ = c.cache.SetWithTTL(ctx, c.cacheKey(s.ID), s, c.ttl)
	return nil
}

func (c *SessionCache) Get(ctx context.Context, id string) (*datastore.Session, error) {
	if c.cache == nil {
		return c.inner.Get(ctx, id)
	}

	var cached datastore.Session
	if err := c.cache.Get(ctx, c.cacheKey(id), &cached); err == nil {
		c.log.Debug("session cache hit", zap.String("id", id))
		return &cached, nil
	} else if err != goredis.Nil {
		c.log.Warn("session cache read error", zap.String("id", id), zap.Error(err))
	}

	sess, err := c.inner.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := c.cache.SetWithTTL(ctx, c.cacheKey(id), sess, c.ttl); err != nil {
		c.log.Warn("session cache write error", zap.String("id", id), zap.Error(err))
	}
	return sess, nil
}

func (c *SessionCache) Touch(ctx context.Context, id string) error {
	// Invalidate cache on touch so the next read gets fresh LastSeenAt.
	_ = c.cache.Delete(ctx, c.cacheKey(id))
	return c.inner.Touch(ctx, id)
}

func (c *SessionCache) Revoke(ctx context.Context, id string) error {
	_ = c.cache.Delete(ctx, c.cacheKey(id))
	return c.inner.Revoke(ctx, id)
}

func (c *SessionCache) DeleteExpired(ctx context.Context) (int, error) {
	return c.inner.DeleteExpired(ctx)
}

func (c *SessionCache) ListForUser(ctx context.Context, userID string) ([]datastore.Session, error) {
	return c.inner.ListForUser(ctx, userID)
}

// Compile-time check that SessionCache implements SessionRepo.
var _ datastore.SessionRepo = (*SessionCache)(nil)
