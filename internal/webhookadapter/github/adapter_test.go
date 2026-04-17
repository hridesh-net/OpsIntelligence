package github

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/webhookadapter"
)

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	t.Parallel()
	body := []byte(`{"action":"opened"}`)
	secret := "s3cret"
	good := sign(body, secret)
	cases := []struct {
		name, header string
		wantErr      bool
	}{
		{"valid", good, false},
		{"missing prefix", strings.TrimPrefix(good, "sha256="), true},
		{"bad hex", "sha256=zzz", true},
		{"mismatched body", sign([]byte("{}"), secret), true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := verifySignature(body, tc.header, secret)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestAdapter_NameAndPath(t *testing.T) {
	t.Parallel()
	a := New(Config{Enabled: true})
	if a.Name() != "github" {
		t.Fatalf("Name()=%q", a.Name())
	}
	if a.Path() != "github" {
		t.Fatalf("Path()=%q default", a.Path())
	}
	a = New(Config{Enabled: true, Path: "ghwh"})
	if a.Path() != "ghwh" {
		t.Fatalf("Path()=%q", a.Path())
	}
}

func TestAdapter_Verify_RespectsAllowUnverified(t *testing.T) {
	t.Parallel()
	a := New(Config{Enabled: true, AllowUnverified: true})
	req := httptest.NewRequest("POST", "/api/webhook/github", bytes.NewReader([]byte(`{}`)))
	if err := a.Verify(req, []byte(`{}`)); err != nil {
		t.Fatalf("allow_unverified should skip verify: %v", err)
	}
}

func TestAdapter_Parse_ExtractsRepoAndSender(t *testing.T) {
	t.Parallel()
	body := []byte(`{"action":"opened","repository":{"full_name":"a/b"},"sender":{"login":"alice"}}`)
	a := New(Config{Enabled: true, AllowUnverified: true})
	req := httptest.NewRequest("POST", "/api/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", "abc")
	e, err := a.Parse(req, body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Kind != "pull_request" || e.Action != "opened" {
		t.Fatalf("kind/action: %+v", e)
	}
	if e.DeliveryID != "abc" {
		t.Fatalf("delivery=%q", e.DeliveryID)
	}
	if e.Repository != "a/b" {
		t.Fatalf("repo=%q", e.Repository)
	}
	if e.Sender != "alice" {
		t.Fatalf("sender=%q", e.Sender)
	}
}

func TestAdapter_Filter_PingIsHealthcheck(t *testing.T) {
	t.Parallel()
	a := New(Config{Enabled: true, AllowUnverified: true})
	fr := a.Filter(webhookadapter.Event{Kind: "ping"})
	if fr.Allowed {
		t.Fatal("ping should be skipped")
	}
	if !strings.HasPrefix(fr.Reason, "healthcheck:") {
		t.Fatalf("reason=%q", fr.Reason)
	}
}

func TestAdapter_Filter_Allowlist(t *testing.T) {
	t.Parallel()
	a := New(Config{
		Enabled: true,
		Events: map[string][]string{
			"pull_request": {"opened", "synchronize"},
			"workflow_run": {}, // any action
			"push":         {}, // no action
		},
	})
	cases := []struct {
		kind, action string
		want         bool
	}{
		{"pull_request", "opened", true},
		{"pull_request", "SYNCHRONIZE", true},
		{"pull_request", "labeled", false},
		{"workflow_run", "completed", true},
		{"push", "", true},
		{"issues", "opened", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.kind+"/"+tc.action, func(t *testing.T) {
			got := a.Filter(webhookadapter.Event{Kind: tc.kind, Action: tc.action}).Allowed
			if got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestRenderTemplate_NestedFields(t *testing.T) {
	t.Parallel()
	e := webhookadapter.Event{
		Kind:       "pull_request",
		Action:     "opened",
		DeliveryID: "dead-beef",
		Payload: map[string]interface{}{
			"action": "opened",
			"repository": map[string]interface{}{
				"full_name": "acme/widgets",
			},
			"pull_request": map[string]interface{}{
				"number": float64(42),
				"user":   map[string]interface{}{"login": "alice"},
				"base":   map[string]interface{}{"ref": "main"},
				"head":   map[string]interface{}{"ref": "feat/frob"},
			},
		},
	}
	tpl := `PR {{.action}} {{.repository.full_name}}#{{.pull_request.number}} by @{{.pull_request.user.login}} ({{.pull_request.base.ref}}<-{{.pull_request.head.ref}})`
	got, err := RenderTemplate(tpl, e)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := `PR opened acme/widgets#42 by @alice (main<-feat/frob)`
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}
