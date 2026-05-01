package msteams

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/channels/adapter"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestChannel(t *testing.T) *Channel {
	t.Helper()
	ch, err := New("app-id-test", "app-password-test", "127.0.0.1:0", "open", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch.WithEmulatorMode()
	return ch
}

func postActivity(t *testing.T, handler http.Handler, act any, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(act)
	if err != nil {
		t.Fatalf("marshal activity: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func simpleActivity(actType, text, convID string) map[string]any {
	return map[string]any{
		"type":        actType,
		"id":          "act-1",
		"serviceUrl":  "https://smba.trafficmanager.net/",
		"text":        text,
		"textFormat":  "plain",
		"from":        map[string]string{"id": "user-1", "name": "Alice"},
		"recipient":   map[string]string{"id": "bot-1", "name": "OpsBot"},
		"conversation": map[string]any{"id": convID, "isGroup": false},
	}
}

// startTestMux wires a minimal /api/messages handler using ch logic and returns a
// function that blocks up to 200 ms for a dispatched event.
func startTestMux(t *testing.T, ch *Channel) (http.Handler, func(*adapter.InboundEvent)) {
	t.Helper()
	var mu sync.Mutex
	var received *adapter.InboundEvent

	handler := func(ctx context.Context, ev adapter.InboundEvent) error {
		mu.Lock()
		cp := ev
		received = &cp
		mu.Unlock()
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","channel":"msteams"}`))
	})
	mux.HandleFunc("/api/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := ch.verifyInboundJWT(r); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var act teamsActivity
		if err := json.NewDecoder(r.Body).Decode(&act); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		ch.cacheServiceURL(act.Conversation.ID, act.ServiceURL)

		switch act.Type {
		case "conversationUpdate":
			ch.handleConversationUpdate(r.Context(), act, handler)
			w.WriteHeader(http.StatusOK)
			return
		case "message":
		default:
			w.WriteHeader(http.StatusOK)
			return
		}

		text := cleanTeamsText(act.Text, act.TextFormat)
		if text == "" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if ch.dmMode == "disabled" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if ch.dmMode == "allowlist" {
			if act.Conversation.IsGroup {
				w.WriteHeader(http.StatusOK)
				return
			}
			if !ch.isAllowed(act.From.ID) {
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		ev := adapter.InboundEvent{
			ChannelID: ch.Name(),
			SessionID: "msteams:" + act.Conversation.ID,
			Text:      text,
		}
		w.WriteHeader(http.StatusOK)
		go func() { _ = handler(r.Context(), ev) }()
	})

	waitEvent := func(out *adapter.InboundEvent) {
		deadline := time.Now().Add(200 * time.Millisecond)
		for time.Now().Before(deadline) {
			mu.Lock()
			r := received
			mu.Unlock()
			if r != nil {
				*out = *r
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return mux, waitEvent
}

// ── New() validation ──────────────────────────────────────────────────────────

func TestNew_RequiresCredentials(t *testing.T) {
	if _, err := New("", "password", "", "", nil); err == nil {
		t.Error("expected error for empty appID")
	}
	if _, err := New("appid", "", "", "", nil); err == nil {
		t.Error("expected error for empty appPassword")
	}
}

func TestNew_DefaultsApplied(t *testing.T) {
	ch, err := New("id", "pw", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ch.listenAddr != ":3978" {
		t.Errorf("listenAddr = %q; want :3978", ch.listenAddr)
	}
	if ch.dmMode != "open" {
		t.Errorf("dmMode = %q; want open", ch.dmMode)
	}
}

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

// ── assertOutboundAllowlisted ─────────────────────────────────────────────────

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

	openCh, _ := New("app", "secret", ":0", "open", []string{"user-a"})
	openCh.convOwnerMu.Lock()
	openCh.convOwner["conv-y"] = "anyone"
	openCh.convOwnerMu.Unlock()
	if err := openCh.assertOutboundAllowlisted("conv-y"); err != nil {
		t.Fatalf("open mode should not block outbound, got %v", err)
	}

	disabledCh, _ := New("app", "secret", ":0", "disabled", nil)
	if err := disabledCh.assertOutboundAllowlisted("conv-z"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
}

// ── parseTeamsSession ─────────────────────────────────────────────────────────

func TestParseTeamsSession(t *testing.T) {
	id, err := parseTeamsSession("msteams:conv-abc-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "conv-abc-123" {
		t.Errorf("id = %q; want conv-abc-123", id)
	}
	if _, err := parseTeamsSession("discord:conv-1"); err == nil {
		t.Error("expected error for wrong prefix")
	}
	if _, err := parseTeamsSession("msteams:"); err == nil {
		t.Error("expected error for empty conv id")
	}
}

// ── cleanTeamsText ────────────────────────────────────────────────────────────

func TestCleanTeamsText(t *testing.T) {
	tests := []struct {
		name, text, textFormat, want string
	}{
		{"empty", "", "plain", ""},
		{"plain passthrough", "hello world", "plain", "hello world"},
		{"mention stripped", "<at>OpsBot</at> what is the status", "xml", "what is the status"},
		{"mention with attrs", `<at id="bot1">OpsBot</at>  deploy now`, "xml", "deploy now"},
		{"multiple mentions", "<at>OpsBot</at> hello <at>SomeBot</at> test", "xml", "hello test"},
		{"html tags removed", "<b>bold</b> and <i>italic</i>", "xml", "bold and italic"},
		{"entities unescaped", "a &amp; b &lt;c&gt;", "xml", "a & b <c>"},
		{"mention in plain", "<at>OpsBot</at> ping", "plain", "ping"},
		{"trim whitespace", "  hello  ", "plain", "hello"},
		{"mention-only empty", "<at>OpsBot</at>", "xml", ""},
		{"case-insensitive AT", "<AT>OpsBot</AT> hi", "xml", "hi"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanTeamsText(tc.text, tc.textFormat)
			if got != tc.want {
				t.Errorf("cleanTeamsText(%q, %q) = %q; want %q", tc.text, tc.textFormat, got, tc.want)
			}
		})
	}
}

// ── health endpoint ───────────────────────────────────────────────────────────

func TestHealthEndpoint(t *testing.T) {
	ch := newTestChannel(t)
	mux, _ := startTestMux(t, ch)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("health status = %d; want 200", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("health body JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q; want ok", body["status"])
	}
	if body["channel"] != "msteams" {
		t.Errorf("channel = %q; want msteams", body["channel"])
	}
}

// ── StartInbound handler dispatch ─────────────────────────────────────────────

func TestStartInbound_MessageDispatched(t *testing.T) {
	ch := newTestChannel(t)
	mux, waitEvent := startTestMux(t, ch)

	rr := postActivity(t, mux, simpleActivity("message", "deploy to prod", "conv-abc"), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var ev adapter.InboundEvent
	waitEvent(&ev)
	if ev.Text != "deploy to prod" {
		t.Errorf("event text = %q; want 'deploy to prod'", ev.Text)
	}
	if ev.SessionID != "msteams:conv-abc" {
		t.Errorf("session = %q; want msteams:conv-abc", ev.SessionID)
	}
}

func TestStartInbound_EmptyTextSkipped(t *testing.T) {
	ch := newTestChannel(t)
	mux, waitEvent := startTestMux(t, ch)

	// mention-only becomes empty after cleaning → no event dispatched
	rr := postActivity(t, mux, simpleActivity("message", "<at>OpsBot</at>", "conv-abc"), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var ev adapter.InboundEvent
	waitEvent(&ev)
	if ev.Text != "" {
		t.Errorf("expected no event; got text %q", ev.Text)
	}
}

func TestStartInbound_UnknownActivityIgnored(t *testing.T) {
	ch := newTestChannel(t)
	mux, waitEvent := startTestMux(t, ch)

	rr := postActivity(t, mux, simpleActivity("typing", "", "conv-abc"), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var ev adapter.InboundEvent
	waitEvent(&ev)
	if ev.Text != "" {
		t.Errorf("unexpected event for typing activity: %q", ev.Text)
	}
}

func TestStartInbound_AllowlistBlocksGroupMessages(t *testing.T) {
	ch, _ := New("app-id", "app-pw", "127.0.0.1:0", "allowlist", []string{"allowed-user"})
	ch.WithEmulatorMode()
	mux, waitEvent := startTestMux(t, ch)

	act := map[string]any{
		"type": "message", "id": "1", "serviceUrl": "https://smba.test/",
		"text": "group message", "textFormat": "plain",
		"from":         map[string]string{"id": "allowed-user", "name": "Alice"},
		"recipient":    map[string]string{"id": "bot-1"},
		"conversation": map[string]any{"id": "conv-grp", "isGroup": true},
	}
	rr := postActivity(t, mux, act, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var ev adapter.InboundEvent
	waitEvent(&ev)
	if ev.Text != "" {
		t.Errorf("expected blocked group message, got event %q", ev.Text)
	}
}

func TestStartInbound_AllowlistBlocksUnknownSender(t *testing.T) {
	ch, _ := New("app-id", "app-pw", "127.0.0.1:0", "allowlist", []string{"allowed-user"})
	ch.WithEmulatorMode()
	mux, waitEvent := startTestMux(t, ch)

	act := simpleActivity("message", "hello", "conv-1")
	act["from"] = map[string]string{"id": "unknown-user", "name": "Unknown"}
	rr := postActivity(t, mux, act, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var ev adapter.InboundEvent
	waitEvent(&ev)
	if ev.Text != "" {
		t.Errorf("expected blocked unknown sender, got event %q", ev.Text)
	}
}

func TestStartInbound_AllowlistPermitsKnownSender(t *testing.T) {
	ch, _ := New("app-id", "app-pw", "127.0.0.1:0", "allowlist", []string{"allowed-user"})
	ch.WithEmulatorMode()
	mux, waitEvent := startTestMux(t, ch)

	act := simpleActivity("message", "authorized message", "conv-1")
	act["from"] = map[string]string{"id": "allowed-user", "name": "Alice"}
	rr := postActivity(t, mux, act, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var ev adapter.InboundEvent
	waitEvent(&ev)
	if ev.Text != "authorized message" {
		t.Errorf("event text = %q; want 'authorized message'", ev.Text)
	}
}

// ── cacheServiceURL ───────────────────────────────────────────────────────────

func TestCacheServiceURL(t *testing.T) {
	ch := newTestChannel(t)
	ch.cacheServiceURL("conv-1", "https://smba.trafficmanager.net/")
	ch.serviceURLsMu.RLock()
	got := ch.serviceURLs["conv-1"]
	ch.serviceURLsMu.RUnlock()
	if got != "https://smba.trafficmanager.net/" {
		t.Errorf("serviceURL = %q", got)
	}
}

func TestCacheServiceURL_EmptyIgnored(t *testing.T) {
	ch := newTestChannel(t)
	ch.cacheServiceURL("", "https://smba.trafficmanager.net/")
	ch.cacheServiceURL("conv-1", "")
	ch.serviceURLsMu.RLock()
	n := len(ch.serviceURLs)
	ch.serviceURLsMu.RUnlock()
	if n != 0 {
		t.Errorf("expected empty serviceURLs, got %d entries", n)
	}
}

// ── conversationUpdate ────────────────────────────────────────────────────────

func TestHandleConversationUpdate_BotAdded(t *testing.T) {
	ch := newTestChannel(t)
	mux, waitEvent := startTestMux(t, ch)

	act := map[string]any{
		"type": "conversationUpdate", "id": "act-update",
		"serviceUrl":   "https://smba.trafficmanager.net/",
		"from":         map[string]string{"id": "user-1", "name": "Alice"},
		"recipient":    map[string]string{"id": "bot-1", "name": "OpsBot"},
		"conversation": map[string]any{"id": "conv-team-1", "isGroup": true},
		"membersAdded": []map[string]string{{"id": "bot-1", "name": "OpsBot"}},
	}
	rr := postActivity(t, mux, act, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var ev adapter.InboundEvent
	waitEvent(&ev)
	if ev.Text != "conversationUpdate:botAdded" {
		t.Errorf("event text = %q; want conversationUpdate:botAdded", ev.Text)
	}
}

func TestHandleConversationUpdate_BotNotAdded_NoEvent(t *testing.T) {
	ch := newTestChannel(t)
	mux, waitEvent := startTestMux(t, ch)

	act := map[string]any{
		"type": "conversationUpdate", "id": "act-update",
		"serviceUrl":   "https://smba.trafficmanager.net/",
		"from":         map[string]string{"id": "user-1"},
		"recipient":    map[string]string{"id": "bot-1"},
		"conversation": map[string]any{"id": "conv-1"},
		// user added, not bot
		"membersAdded": []map[string]string{{"id": "user-2", "name": "Bob"}},
	}
	rr := postActivity(t, mux, act, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var ev adapter.InboundEvent
	waitEvent(&ev)
	if ev.Text != "" {
		t.Errorf("expected no event when bot not in membersAdded, got %q", ev.Text)
	}
}

// ── JWT verification ──────────────────────────────────────────────────────────

func generateTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func makeJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	headerJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	claimsJSON, _ := json.Marshal(claims)
	sigInput := b64url(headerJSON) + "." + b64url(claimsJSON)
	digest := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return sigInput + "." + b64url(sig)
}

func mockJWKSCache(t *testing.T, key *rsa.PrivateKey, kid string) (*jwksCache, func()) {
	t.Helper()
	nBytes := key.PublicKey.N.Bytes()
	eBytes := big.NewInt(int64(key.PublicKey.E)).Bytes()
	jwkJSON, _ := json.Marshal(map[string]any{
		"keys": []map[string]any{{"kty": "RSA", "kid": kid, "n": b64url(nBytes), "e": b64url(eBytes)}},
	})

	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwkJSON)
	}))
	metaJSON, _ := json.Marshal(map[string]string{"jwks_uri": jwksSrv.URL})
	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(metaJSON)
	}))
	cache := &jwksCache{
		metaURL: metaSrv.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
	return cache, func() { metaSrv.Close(); jwksSrv.Close() }
}

func TestVerifyInboundJWT_ValidToken(t *testing.T) {
	key := generateTestKey(t)
	cache, cleanup := mockJWKSCache(t, key, "kid-1")
	defer cleanup()

	ch := &Channel{appID: "my-app-id", jwtCache: cache}
	now := time.Now()
	token := makeJWT(t, key, "kid-1", map[string]any{
		"iss": botFrameworkIssuer, "aud": "my-app-id",
		"exp": now.Add(10 * time.Minute).Unix(),
		"nbf": now.Add(-1 * time.Minute).Unix(),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if err := ch.verifyInboundJWT(req); err != nil {
		t.Errorf("valid token should pass, got: %v", err)
	}
}

func TestVerifyInboundJWT_NilCacheSkipsVerification(t *testing.T) {
	ch := &Channel{appID: "my-app-id", jwtCache: nil}
	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	if err := ch.verifyInboundJWT(req); err != nil {
		t.Errorf("nil cache (emulator mode) should skip verification, got: %v", err)
	}
}

func TestVerifyInboundJWT_MissingHeader(t *testing.T) {
	key := generateTestKey(t)
	cache, cleanup := mockJWKSCache(t, key, "kid-1")
	defer cleanup()
	ch := &Channel{appID: "my-app-id", jwtCache: cache}
	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	if err := ch.verifyInboundJWT(req); err == nil {
		t.Fatal("expected error for missing Authorization header")
	}
}

func TestVerifyInboundJWT_WrongIssuer(t *testing.T) {
	key := generateTestKey(t)
	cache, cleanup := mockJWKSCache(t, key, "kid-1")
	defer cleanup()
	ch := &Channel{appID: "my-app-id", jwtCache: cache}
	now := time.Now()
	token := makeJWT(t, key, "kid-1", map[string]any{
		"iss": "https://evil.example.com", "aud": "my-app-id",
		"exp": now.Add(10 * time.Minute).Unix(), "nbf": now.Add(-1 * time.Minute).Unix(),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	err := ch.verifyInboundJWT(req)
	if err == nil {
		t.Fatal("expected error for wrong issuer")
	}
	if !strings.Contains(err.Error(), "issuer") {
		t.Errorf("error should mention issuer, got: %v", err)
	}
}

func TestVerifyInboundJWT_WrongAudience(t *testing.T) {
	key := generateTestKey(t)
	cache, cleanup := mockJWKSCache(t, key, "kid-1")
	defer cleanup()
	ch := &Channel{appID: "my-app-id", jwtCache: cache}
	now := time.Now()
	token := makeJWT(t, key, "kid-1", map[string]any{
		"iss": botFrameworkIssuer, "aud": "wrong-app-id",
		"exp": now.Add(10 * time.Minute).Unix(), "nbf": now.Add(-1 * time.Minute).Unix(),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	err := ch.verifyInboundJWT(req)
	if err == nil {
		t.Fatal("expected error for wrong audience")
	}
	if !strings.Contains(err.Error(), "audience") {
		t.Errorf("error should mention audience, got: %v", err)
	}
}

func TestVerifyInboundJWT_AudienceArray(t *testing.T) {
	key := generateTestKey(t)
	cache, cleanup := mockJWKSCache(t, key, "kid-1")
	defer cleanup()
	ch := &Channel{appID: "my-app-id", jwtCache: cache}
	now := time.Now()
	// aud as []string containing the app ID
	token := makeJWT(t, key, "kid-1", map[string]any{
		"iss": botFrameworkIssuer, "aud": []string{"other-id", "my-app-id"},
		"exp": now.Add(10 * time.Minute).Unix(), "nbf": now.Add(-1 * time.Minute).Unix(),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if err := ch.verifyInboundJWT(req); err != nil {
		t.Errorf("array aud with matching app_id should pass, got: %v", err)
	}
}

func TestVerifyInboundJWT_ExpiredToken(t *testing.T) {
	key := generateTestKey(t)
	cache, cleanup := mockJWKSCache(t, key, "kid-1")
	defer cleanup()
	ch := &Channel{appID: "my-app-id", jwtCache: cache}
	past := time.Now().Add(-30 * time.Minute)
	token := makeJWT(t, key, "kid-1", map[string]any{
		"iss": botFrameworkIssuer, "aud": "my-app-id",
		"exp": past.Unix(), "nbf": past.Add(-10 * time.Minute).Unix(),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	err := ch.verifyInboundJWT(req)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error should mention expired, got: %v", err)
	}
}

func TestVerifyInboundJWT_NotYetValid(t *testing.T) {
	key := generateTestKey(t)
	cache, cleanup := mockJWKSCache(t, key, "kid-1")
	defer cleanup()
	ch := &Channel{appID: "my-app-id", jwtCache: cache}
	future := time.Now().Add(30 * time.Minute)
	token := makeJWT(t, key, "kid-1", map[string]any{
		"iss": botFrameworkIssuer, "aud": "my-app-id",
		"exp": future.Add(10 * time.Minute).Unix(), "nbf": future.Unix(),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	err := ch.verifyInboundJWT(req)
	if err == nil {
		t.Fatal("expected error for nbf in future")
	}
	if !strings.Contains(err.Error(), "not yet valid") {
		t.Errorf("error should mention not yet valid, got: %v", err)
	}
}

func TestVerifyInboundJWT_BadSignature(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)
	// cache has key1's public key, but token signed with key2
	cache, cleanup := mockJWKSCache(t, key1, "kid-1")
	defer cleanup()
	ch := &Channel{appID: "my-app-id", jwtCache: cache}
	now := time.Now()
	token := makeJWT(t, key2, "kid-1", map[string]any{
		"iss": botFrameworkIssuer, "aud": "my-app-id",
		"exp": now.Add(10 * time.Minute).Unix(), "nbf": now.Add(-1 * time.Minute).Unix(),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	err := ch.verifyInboundJWT(req)
	if err == nil {
		t.Fatal("expected error for bad signature")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Errorf("error should mention signature, got: %v", err)
	}
}

func TestVerifyInboundJWT_MissingKid(t *testing.T) {
	key := generateTestKey(t)
	cache, cleanup := mockJWKSCache(t, key, "kid-1")
	defer cleanup()
	ch := &Channel{appID: "my-app-id", jwtCache: cache}

	// Build token manually with no kid in header
	headerJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claimsJSON, _ := json.Marshal(map[string]any{
		"iss": botFrameworkIssuer, "aud": "my-app-id",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	})
	sigInput := b64url(headerJSON) + "." + b64url(claimsJSON)
	digest := sha256.Sum256([]byte(sigInput))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	token := sigInput + "." + b64url(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/messages", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	err := ch.verifyInboundJWT(req)
	if err == nil {
		t.Fatal("expected error for missing kid")
	}
	if !strings.Contains(err.Error(), "kid") {
		t.Errorf("error should mention kid, got: %v", err)
	}
}

func TestStartInbound_JWTRequired(t *testing.T) {
	// Channel with real jwtCache (not nil) but we send a malformed token → 401
	ch, _ := New("app-id", "app-pw", "127.0.0.1:0", "open", nil)
	// Point cache at a server that won't serve valid keys — token parsing itself fails first
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer badSrv.Close()
	ch.jwtCache = &jwksCache{
		metaURL: badSrv.URL,
		client:  &http.Client{Timeout: 1 * time.Second},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/messages", func(w http.ResponseWriter, r *http.Request) {
		if err := ch.verifyInboundJWT(r); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// Send with a syntactically invalid Bearer token
	rr := postActivity(t, mux, simpleActivity("message", "hello", "conv-1"), "Bearer not.a.real.token")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid JWT, got %d", rr.Code)
	}
}
