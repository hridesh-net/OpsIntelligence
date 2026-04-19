// Package github implements the GitHub webhook adapter for the
// webhookadapter registry. It validates X-Hub-Signature-256 HMAC, parses
// X-GitHub-Event and X-GitHub-Delivery, applies a per-event action
// allow-list, and renders a Go text/template prompt against the full
// nested payload.
package github

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"text/template"

	"github.com/opsintelligence/opsintelligence/internal/webhookadapter"
)

// Config is the adapter-owned configuration carried in the gateway YAML
// under webhooks.adapters.github.
type Config struct {
	Enabled         bool                `yaml:"enabled"`
	Secret          string              `yaml:"secret"`
	Path            string              `yaml:"path,omitempty"`
	Default         string              `yaml:"default_prompt,omitempty"`
	Events          map[string][]string `yaml:"events,omitempty"`
	Prompts         map[string]string   `yaml:"prompts,omitempty"`
	AllowUnverified bool                `yaml:"allow_unverified,omitempty"`
}

// Adapter is the GitHub implementation of webhookadapter.Adapter.
type Adapter struct {
	Cfg Config
}

// New returns a new Adapter. The webhookadapter.Registry calls Name() and
// Path() immediately, so required defaults are materialised here.
func New(cfg Config) *Adapter {
	if strings.TrimSpace(cfg.Path) == "" {
		cfg.Path = "github"
	}
	return &Adapter{Cfg: cfg}
}

// Name implements webhookadapter.Adapter.
func (a *Adapter) Name() string { return "github" }

// Path implements webhookadapter.Adapter.
func (a *Adapter) Path() string {
	p := strings.TrimSpace(a.Cfg.Path)
	if p == "" {
		return "github"
	}
	return p
}

// Enabled implements webhookadapter.Adapter.
func (a *Adapter) Enabled() bool { return a.Cfg.Enabled }

// Verify implements webhookadapter.Adapter. Authenticates via the
// X-Hub-Signature-256 HMAC header (GitHub's standard).
func (a *Adapter) Verify(r *http.Request, body []byte) error {
	if a.Cfg.AllowUnverified {
		return nil
	}
	if strings.TrimSpace(a.Cfg.Secret) == "" {
		return errors.New("github webhook secret not configured")
	}
	sig := strings.TrimSpace(r.Header.Get("X-Hub-Signature-256"))
	return verifySignature(body, sig, a.Cfg.Secret)
}

// Parse implements webhookadapter.Adapter. Reads X-GitHub-Event and
// X-GitHub-Delivery headers and unmarshals the JSON body.
func (a *Adapter) Parse(r *http.Request, body []byte) (webhookadapter.Event, error) {
	kind := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	if kind == "" {
		return webhookadapter.Event{}, errors.New("missing X-GitHub-Event header")
	}
	delivery := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return webhookadapter.Event{}, fmt.Errorf("invalid json: %w", err)
	}
	action, _ := payload["action"].(string)

	repoName := ""
	if repo, ok := payload["repository"].(map[string]interface{}); ok {
		repoName, _ = repo["full_name"].(string)
	}
	senderName := ""
	if sender, ok := payload["sender"].(map[string]interface{}); ok {
		senderName, _ = sender["login"].(string)
	}

	return webhookadapter.Event{
		Source:     "github",
		Kind:       kind,
		Action:     action,
		DeliveryID: delivery,
		Repository: repoName,
		Sender:     senderName,
		Payload:    payload,
		RawBody:    body,
	}, nil
}

// Filter implements webhookadapter.Adapter. Applies the configured event
// allow-list; always ACKs "ping" as a healthcheck so operators can tell
// the endpoint is reachable.
func (a *Adapter) Filter(e webhookadapter.Event) webhookadapter.FilterResult {
	if e.Kind == "ping" {
		return webhookadapter.FilterResult{Allowed: false, Reason: "healthcheck:ping"}
	}
	if len(a.Cfg.Events) == 0 {
		return webhookadapter.FilterResult{Allowed: true}
	}
	actions, ok := a.Cfg.Events[e.Kind]
	if !ok {
		return webhookadapter.FilterResult{Allowed: false, Reason: "event not in allowlist"}
	}
	if len(actions) == 0 {
		return webhookadapter.FilterResult{Allowed: true}
	}
	if _, hasAction := eventsWithoutAction[e.Kind]; hasAction && e.Action == "" {
		return webhookadapter.FilterResult{Allowed: true}
	}
	for _, want := range actions {
		if strings.EqualFold(want, e.Action) {
			return webhookadapter.FilterResult{Allowed: true}
		}
	}
	return webhookadapter.FilterResult{Allowed: false, Reason: "action not in allowlist for event " + e.Kind}
}

// Render implements webhookadapter.Adapter. Templates are Go text/template
// over the full parsed JSON plus four injected keys: .event, .delivery_id,
// .action, .payload_keys.
func (a *Adapter) Render(e webhookadapter.Event) (string, error) {
	tpl := ""
	if v, ok := a.Cfg.Prompts[e.Kind]; ok && strings.TrimSpace(v) != "" {
		tpl = v
	} else if strings.TrimSpace(a.Cfg.Default) != "" {
		tpl = a.Cfg.Default
	} else {
		tpl = DefaultPrompt
	}
	return RenderTemplate(tpl, e)
}

// RenderTemplate is factored out so tests (and future adapters that want
// GitHub-style payload semantics) can render templates directly.
func RenderTemplate(tpl string, e webhookadapter.Event) (string, error) {
	t, err := template.New("github-webhook").Option("missingkey=zero").Parse(tpl)
	if err != nil {
		return "", err
	}
	ctx := map[string]any{}
	for k, v := range e.Payload {
		ctx[k] = v
	}
	ctx["event"] = e.Kind
	ctx["delivery_id"] = e.DeliveryID
	ctx["action"] = e.Action
	ctx["payload_keys"] = topLevelKeys(e.Payload)

	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// verifySignature validates an X-Hub-Signature-256 header against body
// using HMAC-SHA256 and the shared secret. Uses hmac.Equal for a
// constant-time comparison.
func verifySignature(body []byte, header, secret string) error {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return errors.New("missing or malformed X-Hub-Signature-256")
	}
	got, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return fmt.Errorf("signature hex decode: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return errors.New("signature mismatch")
	}
	return nil
}

// eventsWithoutAction is the set of GitHub events whose payloads don't have
// a top-level "action" field; we allowlist them unconditionally when the
// event appears in the configured Events map.
var eventsWithoutAction = map[string]struct{}{
	"push":                {},
	"ping":                {},
	"create":              {},
	"delete":              {},
	"fork":                {},
	"watch":               {},
	"status":              {},
	"workflow_dispatch":   {},
	"deployment":          {},
	"deployment_status":   {},
	"gollum":              {},
	"public":              {},
	"repository_dispatch": {},
}

func topLevelKeys(m map[string]interface{}) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// DefaultPrompt is used when no event-specific template matches. It routes
// the master agent into the right DevOps chain based on the event.
const DefaultPrompt = `A GitHub webhook fired (event={{.event}}, delivery={{.delivery_id}}, action={{.action}}).
Repository: {{with .repository}}{{.full_name}}{{end}}.
Sender: @{{with .sender}}{{.login}}{{end}}.

Inspect the payload below and decide on the right DevOps workflow:
- pull_request / pull_request_review → build pr_url from the payload, call
  devops.github.pull_request + devops.github.pr_diff (then optional CI tools),
  then chain_run id="pr-review" with inputs including pr_url, github_pr_json,
  and truncated github_diff (chains cannot call the GitHub API themselves).
- workflow_run / check_suite / check_run (failed) → run chain_run id="cicd-regression".
- issues / issue_comment → summarise and triage.
- push → check CI status and recent regressions if this is the default branch.
Dispatch independent sub-tasks with subagent_run_async / subagent_run_parallel
so work runs in parallel without cross-contaminating context.

Raw payload keys available: {{.payload_keys}}.`
