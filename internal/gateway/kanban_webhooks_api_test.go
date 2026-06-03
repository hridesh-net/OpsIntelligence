package gateway_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// TestKanbanWebhooks_CRUD covers create / list / get / put / delete.
func TestKanbanWebhooks_CRUD(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)

	cookies := loginAs(t, svc, mux, "wh-owner", "owner-password-long", "role-owner")

	created := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/kanban/webhooks",
		map[string]any{
			"url":    "https://example.invalid/hook",
			"secret": "topsecret",
			"events": "run.completed,run.error",
		}, cookies)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create: no id")
	}
	if created["secret"] != "topsecret" {
		t.Fatalf("secret should echo on create; got %v", created["secret"])
	}

	// List omits secret.
	list := doReqDecode(t, svc, mux, http.MethodGet, "/api/v1/kanban/webhooks", nil, cookies)
	hooks, _ := list["webhooks"].([]any)
	if len(hooks) != 1 {
		t.Fatalf("expected 1 webhook in list; got %d", len(hooks))
	}
	first, _ := hooks[0].(map[string]any)
	if first["secret"] != nil && first["secret"] != "" {
		t.Fatalf("secret must be omitted from list output; got %v", first["secret"])
	}

	// PUT toggles active.
	res := doReq(t, svc, mux, http.MethodPut, "/api/v1/kanban/webhooks/"+id,
		map[string]any{"active": false}, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT: status=%d body=%s", res.StatusCode, readBody(res))
	}
	var got datastore.KanbanWebhook
	_ = json.NewDecoder(res.Body).Decode(&got)
	if got.Active {
		t.Fatalf("active should be false after PUT")
	}

	// DELETE
	res = doReq(t, svc, mux, http.MethodDelete, "/api/v1/kanban/webhooks/"+id, nil, cookies)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: status=%d", res.StatusCode)
	}
}

// TestKanbanWebhooks_TestEndpoint fires the synchronous /test ping at
// an httptest backend, verifies headers + HMAC signature, and confirms
// last_status round-trips into the row.
func TestKanbanWebhooks_TestEndpoint(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)

	cookies := loginAs(t, svc, mux, "wh-test", "owner-password-long", "role-owner")

	var (
		mu        sync.Mutex
		gotSig    string
		gotEvent  string
		gotBody   []byte
		hitCount  int
	)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		hitCount++
		gotSig = r.Header.Get("X-OpsIntel-Signature")
		gotEvent = r.Header.Get("X-OpsIntel-Event")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	created := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/kanban/webhooks",
		map[string]any{
			"url":    backend.URL,
			"secret": "sssh",
			"events": "*",
		}, cookies)
	id, _ := created["id"].(string)

	res := doReq(t, svc, mux, http.MethodPost,
		"/api/v1/kanban/webhooks/"+id+"/test", nil, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("test endpoint: status=%d body=%s", res.StatusCode, readBody(res))
	}

	mu.Lock()
	defer mu.Unlock()
	if hitCount != 1 {
		t.Fatalf("backend hit %d times; want 1", hitCount)
	}
	if gotEvent != "webhook.ping" {
		t.Fatalf("wrong event header: %q", gotEvent)
	}
	expectedMac := hmac.New(sha256.New, []byte("sssh"))
	expectedMac.Write(gotBody)
	expected := "sha256=" + hex.EncodeToString(expectedMac.Sum(nil))
	if gotSig != expected {
		t.Fatalf("signature mismatch:\n  got      %s\n  expected %s", gotSig, expected)
	}
}
