package main

import (
	"fmt"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

func formatChannelTokenError(label string, err error) string {
	if err == nil {
		return ""
	}
	return sanitizeDoctorMessage(fmt.Sprintf("%s: %v", label, err))
}

// checkWebhooks documents gateway requirements for incoming webhooks (doctor does not start the HTTP server).
func checkWebhooks(cfg *config.Config) doctorCheck {
	if cfg == nil || !cfg.Webhooks.Enabled {
		return doctorCheck{
			ID:       "webhooks.gateway",
			Severity: "skipped",
			Message:  "webhooks.enabled is false (no incoming webhook routes).",
		}
	}
	if len(cfg.Webhooks.Mappings) == 0 {
		return doctorCheck{
			ID:       "webhooks.gateway",
			Severity: "skipped",
			Message:  "webhooks enabled but no mappings; add webhooks.mappings entries.",
		}
	}
	return doctorCheck{
		ID:       "webhooks.gateway",
		Severity: "skipped",
		Message:  "Incoming webhooks require a running gateway and a reachable public URL. Doctor does not start the server — after `opsintelligence gateway start`, verify your ingress or curl the webhook path. See doc/runbooks/doctor-config-validation.md.",
	}
}

// validateSlackTokenFormats checks Bot User OAuth Token and App-level token prefixes.
func validateSlackTokenFormats(botToken, appToken string) error {
	b := strings.TrimSpace(botToken)
	a := strings.TrimSpace(appToken)
	if b == "" || a == "" {
		return fmt.Errorf("bot_token and app_token are required")
	}
	if !strings.HasPrefix(b, "xoxb-") {
		return fmt.Errorf("bot_token should start with xoxb- (Bot User OAuth Token from Slack app OAuth & Permissions)")
	}
	if len(b) < 20 {
		return fmt.Errorf("bot_token looks too short")
	}
	if !strings.HasPrefix(a, "xapp-") {
		return fmt.Errorf("app_token should start with xapp- (App-level token with connections:write for Socket Mode)")
	}
	if len(a) < 20 {
		return fmt.Errorf("app_token looks too short")
	}
	return nil
}

// formatChannelPingError turns API/adapter errors into sanitized, actionable messages (never echoes tokens).
func formatChannelPingError(channelID, step string, err error) string {
	if err == nil {
		return ""
	}
	msg := sanitizeDoctorMessage(err.Error())
	if msg == "" {
		msg = "unknown error"
	}
	lower := strings.ToLower(msg)
	hint := ""
	if channelID == "channel.slack" {
		switch {
		case strings.Contains(lower, "401") || strings.Contains(lower, "invalid_auth"):
			hint = " — invalid bot or app token; reinstall app to workspace or rotate tokens"
		case strings.Contains(lower, "403"):
			hint = " — missing OAuth scopes; ensure chat:write, app_mentions:read, and Socket Mode scopes as needed"
		}
	}
	if strings.Contains(lower, "403") && hint == "" {
		hint = " — forbidden; check token scopes and app install"
	}
	return fmt.Sprintf("%s: %s%s", step, msg, hint)
}
