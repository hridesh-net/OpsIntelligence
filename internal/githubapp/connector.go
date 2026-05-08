package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// ConnectorConfig is the client-side configuration that tells a self-hosted
// OpsIntelligence instance how to dial back to the GitHub App relay.
//
// Set this in opsintelligence.yaml under github_app_connector when the
// operator wants their instance to receive events pushed from the relay
// rather than exposing a public webhook URL.
type ConnectorConfig struct {
	// Enabled activates the outbound connector. Default false.
	Enabled bool `yaml:"enabled"`

	// RelayURL is the base URL of the OpsIntelligence instance that hosts
	// the GitHub App relay (e.g. "https://relay.opsintelligence.example.com").
	// The connector dials wss://<RelayURL>/api/github-app/connect.
	RelayURL string `yaml:"relay_url"`

	// InstallationID is the numeric GitHub App installation_id for this org.
	// Shown on the GitHub App setup page.
	InstallationID int64 `yaml:"installation_id"`

	// ConnectToken is the one-time token generated on the setup page.
	// Store in an env var and reference it here with ${ENV_VAR}.
	ConnectToken string `yaml:"connect_token"`

	// ReconnectInterval is how long to wait between reconnect attempts.
	// Default: 10s.
	ReconnectInterval string `yaml:"reconnect_interval"`
}

func (c ConnectorConfig) reconnectInterval() time.Duration {
	if c.ReconnectInterval != "" {
		if d, err := time.ParseDuration(c.ReconnectInterval); err == nil {
			return d
		}
	}
	return 10 * time.Second
}

// Connector is the outbound WebSocket client. It runs inside the org's
// OpsIntelligence instance and dials the relay, receiving GitHub events
// that are then dispatched to the local agent runner.
type Connector struct {
	cfg     ConnectorConfig
	handler func(ctx context.Context, env *EventEnvelope) error
	log     *zap.Logger
}

// NewConnector creates a Connector. handler is called for every event the
// relay pushes; it should invoke the local agent runner.
func NewConnector(cfg ConnectorConfig, handler func(context.Context, *EventEnvelope) error, log *zap.Logger) *Connector {
	if log == nil {
		log = zap.NewNop()
	}
	return &Connector{cfg: cfg, handler: handler, log: log}
}

// Run connects to the relay and processes events until ctx is cancelled.
// It automatically reconnects on network errors with exponential backoff.
func (c *Connector) Run(ctx context.Context) {
	base := c.cfg.reconnectInterval()
	backoff := base

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := c.runOnce(ctx); err != nil {
			c.log.Warn("githubapp connector: disconnected, will reconnect",
				zap.Duration("backoff", backoff),
				zap.Error(err))
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			if backoff < 5*time.Minute {
				backoff = min(backoff*2, 5*time.Minute)
			}
		}
	}
}

func (c *Connector) runOnce(ctx context.Context) error {
	relayURL := strings.TrimRight(c.cfg.RelayURL, "/")
	// Convert http(s):// to ws(s)://
	wsURL := strings.Replace(relayURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	wsURL = fmt.Sprintf("%s/api/github-app/connect?installation_id=%d&token=%s",
		wsURL, c.cfg.InstallationID, c.cfg.ConnectToken)

	headers := http.Header{}
	headers.Set("User-Agent", "OpsIntelligence-Connector/1.0")

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 15 * time.Second

	conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("connect %s: HTTP %d", wsURL, resp.StatusCode)
		}
		return fmt.Errorf("dial %s: %w", wsURL, err)
	}
	defer conn.Close()

	c.log.Info("githubapp connector: connected to relay",
		zap.String("relay", c.cfg.RelayURL),
		zap.Int64("installation_id", c.cfg.InstallationID))

	// Reset backoff on successful connection.
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})
	conn.SetReadDeadline(time.Now().Add(90 * time.Second)) //nolint:errcheck

	for {
		select {
		case <-ctx.Done():
			conn.WriteMessage(websocket.CloseMessage, //nolint:errcheck
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"))
			return nil
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var env EventEnvelope
		if err := json.Unmarshal(msg, &env); err != nil {
			c.log.Warn("githubapp connector: bad message", zap.Error(err))
			continue
		}

		c.log.Info("githubapp connector: event received",
			zap.String("event", env.Event),
			zap.String("delivery_id", env.DeliveryID),
			zap.String("repository", env.Repository))

		// Dispatch in a goroutine so the WebSocket read loop stays responsive.
		go func(e EventEnvelope) {
			dispatchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if err := c.handler(dispatchCtx, &e); err != nil {
				c.log.Error("githubapp connector: event handler failed",
					zap.String("event", e.Event),
					zap.String("delivery_id", e.DeliveryID),
					zap.Error(err))
			}
		}(env)
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
