package msteams

import (
	"strings"
	"testing"
)

func TestNew_DefaultsToAllowlistWhenAllowFromSet(t *testing.T) {
	ch, err := New("app", "secret", ":0", "", []string{"  user-a  ", ""})
	if err != nil {
		t.Fatal(err)
	}
	if ch.dmMode != "allowlist" {
		t.Fatalf("dmMode = %q, want allowlist", ch.dmMode)
	}
	if len(ch.allowFrom) != 1 || ch.allowFrom[0] != "user-a" {
		t.Fatalf("allowFrom = %#v", ch.allowFrom)
	}
}

func TestAssertOutboundAllowlisted(t *testing.T) {
	ch, err := New("app", "secret", ":0", "allowlist", []string{"user-a", "user-b"})
	if err != nil {
		t.Fatal(err)
	}

	if err := ch.assertOutboundAllowlisted("conv-x"); err == nil || !strings.Contains(err.Error(), "no allowlisted inbound") {
		t.Fatalf("expected no-owner error, got %v", err)
	}

	ch.convOwnerMu.Lock()
	ch.convOwner["conv-x"] = "stranger"
	ch.convOwnerMu.Unlock()
	if err := ch.assertOutboundAllowlisted("conv-x"); err == nil || !strings.Contains(err.Error(), "not in allow_from") {
		t.Fatalf("expected not-in-allowlist error, got %v", err)
	}

	ch.convOwnerMu.Lock()
	ch.convOwner["conv-x"] = "user-b"
	ch.convOwnerMu.Unlock()
	if err := ch.assertOutboundAllowlisted("conv-x"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	openCh, err := New("app", "secret", ":0", "open", []string{"user-a"})
	if err != nil {
		t.Fatal(err)
	}
	openCh.convOwnerMu.Lock()
	openCh.convOwner["conv-y"] = "anyone"
	openCh.convOwnerMu.Unlock()
	if err := openCh.assertOutboundAllowlisted("conv-y"); err != nil {
		t.Fatalf("open mode should not block outbound, got %v", err)
	}

	disabledCh, err := New("app", "secret", ":0", "disabled", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := disabledCh.assertOutboundAllowlisted("conv-z"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
}
