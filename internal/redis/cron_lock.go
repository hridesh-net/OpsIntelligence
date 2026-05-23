package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CronLock provides distributed locking for cron jobs using Redis SET NX.
// Only one OpsIntelligence instance in a cluster will acquire the lock for
// a given job ID, preventing duplicate execution.
type CronLock struct {
	client *Client
	log    *zap.Logger
	nodeID string
}

// NewCronLock returns a distributed lock backed by Redis.
// If client is nil/disabled, all Lock/Unlock calls become no-ops.
func NewCronLock(client *Client, log *zap.Logger) *CronLock {
	if log == nil {
		log = zap.NewNop()
	}
	return &CronLock{
		client: client,
		log:    log,
		nodeID: uuid.New().String()[:8],
	}
}

// Enabled reports whether distributed locking is functional.
func (c *CronLock) Enabled() bool { return c.client != nil && c.client.Enabled() }

// Lock attempts to acquire a lock for the given job ID with the specified TTL.
// Returns true if the lock was acquired, false if another instance holds it.
func (c *CronLock) Lock(ctx context.Context, jobID string, ttl time.Duration) bool {
	if !c.Enabled() {
		return true // No Redis = no coordination needed, always allow locally.
	}
	key := c.client.Key("cron:lock", jobID)
	ok, err := c.client.Client().SetNX(ctx, key, c.nodeID, ttl).Result()
	if err != nil {
		c.log.Warn("cron lock: redis error", zap.String("job", jobID), zap.Error(err))
		return true // Fail open on Redis errors so jobs don't stall.
	}
	if ok {
		c.log.Debug("cron lock acquired", zap.String("job", jobID), zap.String("node", c.nodeID))
	} else {
		c.log.Debug("cron lock contested", zap.String("job", jobID))
	}
	return ok
}

// Unlock releases the lock for the given job ID.
// It uses a Lua script to ensure only the owner can delete the key.
func (c *CronLock) Unlock(ctx context.Context, jobID string) {
	if !c.Enabled() {
		return
	}
	key := c.client.Key("cron:lock", jobID)
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`
	_, err := c.client.Client().Eval(ctx, script, []string{key}, c.nodeID).Result()
	if err != nil {
		c.log.Warn("cron unlock: redis error", zap.String("job", jobID), zap.Error(err))
	}
}

// WithLock runs fn only if the distributed lock is acquired. The lock is
// held for the duration of fn plus a small safety margin. If the lock is
// not acquired, fn is skipped and a log message is emitted.
func (c *CronLock) WithLock(ctx context.Context, jobID string, ttl time.Duration, fn func()) {
	if !c.Lock(ctx, jobID, ttl) {
		c.log.Info("cron job skipped: held by another instance", zap.String("job", jobID))
		return
	}
	defer c.Unlock(ctx, jobID)
	fn()
}

// NodeID returns the unique identifier of this instance used for lock ownership.
func (c *CronLock) NodeID() string { return c.nodeID }

// String returns a human-readable representation for logging.
func (c *CronLock) String() string {
	return fmt.Sprintf("CronLock(node=%s, enabled=%v)", c.nodeID, c.Enabled())
}
