package githubapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Relay forwards a raw GitHub App webhook payload to an org's on-premise
// OpsIntelligence instance. It re-signs the body with the installation's
// configured OpsWebhookSecret so the receiving webhook adapter can verify it.
type Relay struct {
	client *http.Client
}

// NewRelay returns a Relay with a sensible timeout.
func NewRelay() *Relay {
	return &Relay{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// RelayRequest carries everything needed to forward one webhook delivery.
type RelayRequest struct {
	// Endpoint is the org's OpsIntelligence base URL (e.g. "https://opi.acme.internal").
	Endpoint string
	// WebhookSecret is the org's webhooks.adapters.github.secret value. Used to
	// compute X-Hub-Signature-256 so the receiving instance can verify the payload.
	WebhookSecret string
	// Event is the X-GitHub-Event header value (e.g. "pull_request").
	Event string
	// DeliveryID is the X-GitHub-Delivery header value.
	DeliveryID string
	// Body is the raw JSON payload from GitHub.
	Body []byte
	// InstallationID is the numeric GitHub installation_id (for correlation logs).
	InstallationID int64
}

// Forward sends the payload to <endpoint>/api/webhook/github (the standard
// OpsIntelligence GitHub webhook path). It re-signs the body and carries the
// original GitHub event headers so the receiving adapter processes it normally.
//
// Returns an error only when the relay itself fails; a non-2xx response from
// the org's instance is logged but not treated as fatal (the org's instance
// may be temporarily unavailable).
func (r *Relay) Forward(ctx context.Context, req RelayRequest) error {
	endpoint := strings.TrimRight(req.Endpoint, "/")
	url := endpoint + "/api/webhook/github"

	sig := computeSignature(req.Body, req.WebhookSecret)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(req.Body))
	if err != nil {
		return fmt.Errorf("githubapp relay: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-GitHub-Event", req.Event)
	httpReq.Header.Set("X-GitHub-Delivery", req.DeliveryID)
	httpReq.Header.Set("X-Hub-Signature-256", "sha256="+sig)
	httpReq.Header.Set("User-Agent", "OpsIntelligence-GitHubApp-Relay/1.0")

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("githubapp relay: forward to %s: %w", url, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	if resp.StatusCode >= 500 {
		return fmt.Errorf("githubapp relay: %s returned %d", url, resp.StatusCode)
	}
	return nil
}

// computeSignature returns the hex-encoded HMAC-SHA256 of body using secret.
func computeSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
