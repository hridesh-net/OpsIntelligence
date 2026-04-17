package main

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSlackTokenFormats(t *testing.T) {
	t.Parallel()
	if err := validateSlackTokenFormats("xoxb-"+strings.Repeat("x", 30), "xapp-"+strings.Repeat("y", 30)); err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	if err := validateSlackTokenFormats("bad", "xapp-yyy"); err == nil {
		t.Fatal("expected error for bad bot token")
	}
}

func TestFormatChannelPingError_Slack401(t *testing.T) {
	t.Parallel()
	err := errors.New(`slack auth.test failed: invalid_auth`)
	m := formatChannelPingError("channel.slack", "auth.test", err)
	if !strings.Contains(m, "invalid_auth") {
		t.Fatalf("expected original error: %q", m)
	}
	if !strings.Contains(strings.ToLower(m), "token") {
		t.Fatalf("expected token hint: %q", m)
	}
}
