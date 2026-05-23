// Package redis provides an optional Redis integration for OpsIntelligence.
// All features gracefully degrade to in-process fallbacks when Redis is disabled
// or unreachable, keeping the binary lightweight for single-node deployments.
package redis

import (
	"context"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Client wraps go-redis with OpsIntelligence-specific helpers.
type Client struct {
	client  goredis.UniversalClient
	cfg     Config
	log     *zap.Logger
	enabled bool
}

// Config is a slimmed-down version of config.RedisConfig used internally
// so this package doesn't import internal/config (avoiding import cycles).
type Config struct {
	Enabled       bool
	Addr          string
	Password      string
	DB            int
	Addrs         []string
	MasterName    string
	KeyPrefix     string
	CacheTTL      time.Duration
	PubSubChannel string
	MaxRetries    int
}

// NewClient creates a Redis client from config. Returns nil (and no error)
// when Redis is disabled, so callers can safely ignore the result.
func NewClient(cfg Config, log *zap.Logger) (*Client, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "opsintelligence:"
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 5 * time.Minute
	}
	if cfg.PubSubChannel == "" {
		cfg.PubSubChannel = "opsintelligence:events"
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}

	var client goredis.UniversalClient
	opts := &goredis.UniversalOptions{
		Password:   cfg.Password,
		DB:         cfg.DB,
		MaxRetries: cfg.MaxRetries,
	}

	switch {
	case cfg.MasterName != "" && len(cfg.Addrs) > 0:
		opts.Addrs = cfg.Addrs
		client = goredis.NewFailoverClient(&goredis.FailoverOptions{
			MasterName:    cfg.MasterName,
			SentinelAddrs: cfg.Addrs,
			Password:      cfg.Password,
			DB:            cfg.DB,
			MaxRetries:    cfg.MaxRetries,
		})
	case len(cfg.Addrs) > 0:
		opts.Addrs = cfg.Addrs
		client = goredis.NewClusterClient(&goredis.ClusterOptions{
			Addrs:      cfg.Addrs,
			Password:   cfg.Password,
			MaxRetries: cfg.MaxRetries,
		})
	default:
		if cfg.Addr == "" {
			cfg.Addr = "localhost:6379"
		}
		opts.Addrs = []string{cfg.Addr}
		client = goredis.NewClient(&goredis.Options{
			Addr:       cfg.Addr,
			Password:   cfg.Password,
			DB:         cfg.DB,
			MaxRetries: cfg.MaxRetries,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis: ping failed: %w", err)
	}

	if log == nil {
		log = zap.NewNop()
	}
	log.Info("redis connected",
		zap.String("addr", cfg.Addr),
		zap.Strings("addrs", cfg.Addrs),
		zap.String("master_name", cfg.MasterName),
		zap.String("key_prefix", cfg.KeyPrefix),
	)

	return &Client{
		client:  client,
		cfg:     cfg,
		log:     log,
		enabled: true,
	}, nil
}

// Enabled reports whether Redis is active.
func (c *Client) Enabled() bool { return c != nil && c.enabled }

// Close closes the underlying Redis client.
func (c *Client) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

// Key returns a prefixed Redis key.
func (c *Client) Key(parts ...string) string {
	if c == nil {
		return ""
	}
	return c.cfg.KeyPrefix + strings.Join(parts, ":")
}

// Client returns the underlying go-redis client for advanced use.
func (c *Client) Client() goredis.UniversalClient {
	if c == nil {
		return nil
	}
	return c.client
}

// Ping checks connectivity.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Ping(ctx).Err()
}

// Config returns the client configuration.
func (c *Client) Config() Config {
	if c == nil {
		return Config{}
	}
	return c.cfg
}
