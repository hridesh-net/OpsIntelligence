package githubapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Handler mounts two HTTP surfaces for the GitHub App:
//
//   - POST /api/github-app/webhook — receives webhook deliveries from GitHub.
//     Handles installation lifecycle events and relays all other events to
//     the org's configured on-premise OpsIntelligence endpoint (or processes
//     them locally when no endpoint is configured).
//
//   - GET  /api/github-app/setup?installation_id={id} — post-install setup page
//     shown to org admins after they install the App. They provide their
//     on-premise OpsIntelligence endpoint URL and webhook secret here.
//
//   - POST /api/github-app/setup — persists the endpoint configuration.
//
//   - GET  /api/github-app/installations — JSON list of all installations
//     (authenticated endpoint, suitable for the dashboard).
type Handler struct {
	cfg        Config
	store      InstallationRepo
	tokenStore ConnectTokenRepo
	hub        *Hub
	relay      *Relay
	client     *AppClient // nil when private key not configured
	log        *zap.Logger

	// LocalRunner, when non-nil, is called for events on installations that
	// have no connected WebSocket and no configured OpsEndpoint.
	LocalRunner func(ctx context.Context, event, deliveryID string, payload []byte) error
}

// New constructs a Handler. store and tokenStore must not be nil.
// appClient may be nil (setup page skips GitHub API verification).
// localRunner may be nil (events without a connection or endpoint are dropped).
func New(cfg Config, store InstallationRepo, tokenStore ConnectTokenRepo, appClient *AppClient, localRunner func(context.Context, string, string, []byte) error, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		cfg:         cfg,
		store:       store,
		tokenStore:  tokenStore,
		hub:         NewHub(log),
		relay:       NewRelay(),
		client:      appClient,
		log:         log,
		LocalRunner: localRunner,
	}
}

// Mount registers all GitHub App routes on mux. Call this once during server
// startup. The paths follow the pattern /api/<cfg.WebhookPath> etc.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/api/"+h.cfg.webhookPath(), h.handleWebhook)
	mux.HandleFunc("/api/"+h.cfg.setupPath(), h.handleSetup)
	mux.HandleFunc("/api/github-app/installations", h.handleInstallations)
	// WebSocket endpoint: client's OpsIntelligence dials here to receive events.
	mux.HandleFunc("/api/github-app/connect", h.handleConnect)
}

// ─────────────────────────────────────────────────────────────────────────────
// Webhook handler
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body"})
		return
	}

	if err := h.verifySignature(r, body); err != nil {
		h.log.Warn("githubapp: webhook signature invalid", zap.Error(err))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
		return
	}

	event := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	deliveryID := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))

	if event == "ping" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "pong"})
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	instID, _ := extractInstallationID(payload)

	h.log.Info("githubapp: webhook received",
		zap.String("event", event),
		zap.String("delivery_id", deliveryID),
		zap.Int64("installation_id", instID),
	)

	switch event {
	case "installation":
		h.handleInstallationEvent(w, r.Context(), payload, instID, deliveryID)
		return
	case "installation_repositories":
		h.handleInstallationReposEvent(r.Context(), payload, instID, deliveryID)
	}

	// For all other events: relay to org's endpoint or run locally.
	go h.dispatchEvent(r.Context(), instID, event, deliveryID, body)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":          "accepted",
		"delivery_id":     deliveryID,
		"event":           event,
		"installation_id": strconv.FormatInt(instID, 10),
	})
}

func (h *Handler) handleInstallationEvent(w http.ResponseWriter, ctx context.Context, payload map[string]interface{}, instID int64, deliveryID string) {
	action, _ := payload["action"].(string)
	account := extractAccount(payload)

	switch action {
	case "created", "new_permissions_accepted":
		inst := &Installation{
			ID:          instID,
			AccountLogin: account.login,
			AccountType:  account.typ,
			Active:      true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := h.store.Upsert(ctx, inst); err != nil {
			h.log.Error("githubapp: upsert installation", zap.Error(err), zap.Int64("id", instID))
		} else {
			h.log.Info("githubapp: installation created",
				zap.String("account", account.login),
				zap.Int64("installation_id", instID),
			)
		}
		// Redirect admin to setup page.
		setupURL := strings.TrimRight(h.cfg.PublicURL, "/") + "/api/" + h.cfg.setupPath() +
			"?installation_id=" + strconv.FormatInt(instID, 10)
		writeJSON(w, http.StatusOK, map[string]string{
			"status":    "installed",
			"setup_url": setupURL,
		})

	case "deleted":
		if err := h.store.SetActive(ctx, instID, false); err != nil {
			h.log.Warn("githubapp: deactivate installation", zap.Error(err))
		}
		h.log.Info("githubapp: installation deleted", zap.Int64("installation_id", instID))
		writeJSON(w, http.StatusOK, map[string]string{"status": "uninstalled"})

	case "suspend":
		_ = h.store.SetActive(ctx, instID, false)
		h.log.Info("githubapp: installation suspended", zap.Int64("installation_id", instID))
		writeJSON(w, http.StatusOK, map[string]string{"status": "suspended"})

	case "unsuspend":
		_ = h.store.SetActive(ctx, instID, true)
		h.log.Info("githubapp: installation unsuspended", zap.Int64("installation_id", instID))
		writeJSON(w, http.StatusOK, map[string]string{"status": "unsuspended"})

	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (h *Handler) handleInstallationReposEvent(ctx context.Context, payload map[string]interface{}, instID int64, deliveryID string) {
	action, _ := payload["action"].(string)
	h.log.Info("githubapp: installation_repositories event",
		zap.String("action", action),
		zap.Int64("installation_id", instID),
		zap.String("delivery_id", deliveryID),
	)
	// Repo add/remove doesn't require config changes; just log.
}

func (h *Handler) dispatchEvent(ctx context.Context, instID int64, event, deliveryID string, body []byte) {
	if instID == 0 {
		h.log.Warn("githubapp: no installation_id in payload, skipping dispatch",
			zap.String("event", event),
			zap.String("delivery_id", deliveryID),
		)
		return
	}

	// ── Priority 1: active WebSocket connection (on-premise, no public URL needed) ──
	if h.hub.Connected(instID) {
		action, repo := extractActionRepo(body)
		env := &EventEnvelope{
			DeliveryID: deliveryID,
			Event:      event,
			Action:     action,
			Repository: repo,
			Payload:    body,
			ReceivedAt: time.Now(),
		}
		if h.hub.Push(instID, env) {
			h.log.Info("githubapp: event pushed via WebSocket",
				zap.Int64("installation_id", instID),
				zap.String("event", event),
				zap.String("delivery_id", deliveryID),
			)
			return
		}
		// Push returned false (client disconnected mid-send); fall through.
	}

	inst, err := h.store.Get(ctx, instID)
	if err != nil {
		h.log.Info("githubapp: installation not found or lookup error, processing locally",
			zap.Int64("id", instID), zap.Error(err))
		h.runLocal(ctx, event, deliveryID, body)
		return
	}

	if !inst.Active {
		h.log.Info("githubapp: skipping event for inactive installation",
			zap.Int64("id", instID),
			zap.String("event", event),
		)
		return
	}

	// ── Priority 2: HTTP relay to org's public OpsIntelligence endpoint ──
	if inst.OpsEndpoint != "" {
		err := h.relay.Forward(ctx, RelayRequest{
			Endpoint:       inst.OpsEndpoint,
			WebhookSecret:  inst.OpsWebhookSecret,
			Event:          event,
			DeliveryID:     deliveryID,
			Body:           body,
			InstallationID: instID,
		})
		if err != nil {
			h.log.Error("githubapp: relay failed",
				zap.String("account", inst.AccountLogin),
				zap.String("endpoint", inst.OpsEndpoint),
				zap.Error(err),
			)
		} else {
			h.log.Info("githubapp: event relayed via HTTP",
				zap.String("account", inst.AccountLogin),
				zap.String("endpoint", inst.OpsEndpoint),
				zap.String("event", event),
				zap.String("delivery_id", deliveryID),
			)
		}
		return
	}

	// ── Priority 3: process locally on this instance ──
	h.runLocal(ctx, event, deliveryID, body)
}

// extractActionRepo pulls action and repository.full_name from a raw payload.
func extractActionRepo(body []byte) (action, repo string) {
	var partial struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	_ = json.Unmarshal(body, &partial)
	return partial.Action, partial.Repository.FullName
}

func (h *Handler) runLocal(ctx context.Context, event, deliveryID string, body []byte) {
	if h.LocalRunner == nil {
		h.log.Warn("githubapp: no local runner configured, dropping event",
			zap.String("event", event),
			zap.String("delivery_id", deliveryID),
		)
		return
	}
	if err := h.LocalRunner(ctx, event, deliveryID, body); err != nil {
		h.log.Error("githubapp: local runner failed",
			zap.String("event", event),
			zap.String("delivery_id", deliveryID),
			zap.Error(err),
		)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Setup handler (post-install configuration page)
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleSetup(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleSetupGET(w, r)
	case http.MethodPost:
		h.handleSetupPOST(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *Handler) handleSetupGET(w http.ResponseWriter, r *http.Request) {
	instIDStr := r.URL.Query().Get("installation_id")
	instID, _ := strconv.ParseInt(instIDStr, 10, 64)

	// Try to get existing config from DB.
	inst, _ := h.store.Get(r.Context(), instID)

	var currentEndpoint, accountLabel string
	if inst != nil {
		currentEndpoint = inst.OpsEndpoint
		accountLabel = inst.AccountLogin
		if inst.AccountType != "" {
			accountLabel += " (" + inst.AccountType + ")"
		}
	}

	// If we have an AppClient and no DB record yet, verify with GitHub API
	// to get the account name and auto-create the installation record.
	if inst == nil && h.client != nil && instID > 0 {
		login, typ, err := h.client.VerifyInstallation(r.Context(), instID)
		if err != nil {
			h.log.Warn("githubapp: setup verify installation failed", zap.Error(err), zap.Int64("id", instID))
		} else {
			accountLabel = login + " (" + typ + ")"
			// Auto-create the installation record so the webhook handler
			// can dispatch events even before the form is submitted.
			_ = h.store.Upsert(r.Context(), &Installation{
				ID:          instID,
				AccountLogin: login,
				AccountType:  typ,
				Active:      true,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			})
		}
	}

	if accountLabel == "" {
		accountLabel = "installation #" + instIDStr
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, setupPageHTML,
		html.EscapeString(accountLabel),
		instIDStr,
		html.EscapeString(currentEndpoint),
		instIDStr,
	)
}

func (h *Handler) handleSetupPOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid form data"})
		return
	}

	instIDStr := r.FormValue("installation_id")
	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil || instID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation_id"})
		return
	}

	endpoint := strings.TrimRight(strings.TrimSpace(r.FormValue("ops_endpoint")), "/")
	webhookSecret := strings.TrimSpace(r.FormValue("ops_webhook_secret"))
	mode := r.FormValue("mode") // "websocket" or "http"

	if endpoint != "" && !strings.HasPrefix(endpoint, "http") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ops_endpoint must be an http/https URL"})
		return
	}

	// Save endpoint config (may be empty when using WebSocket mode).
	if err := h.store.SetEndpoint(r.Context(), instID, endpoint, webhookSecret); err != nil {
		h.log.Error("githubapp: set endpoint", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save configuration"})
		return
	}

	// Always generate a fresh connect token so the client can use either
	// WebSocket or HTTP relay — both are supported simultaneously.
	connectToken, err := GenerateToken()
	if err != nil {
		h.log.Error("githubapp: generate connect token", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate connect token"})
		return
	}
	if err := h.tokenStore.Upsert(r.Context(), &ConnectToken{
		InstallationID: instID,
		Token:          connectToken,
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(100 * 365 * 24 * time.Hour), // effectively no expiry
	}); err != nil {
		h.log.Error("githubapp: save connect token", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save connect token"})
		return
	}

	h.log.Info("githubapp: installation configured",
		zap.Int64("installation_id", instID),
		zap.String("mode", mode),
		zap.String("endpoint", endpoint),
	)

	relayBase := strings.TrimRight(h.cfg.PublicURL, "/")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Replace placeholders manually to avoid %% escaping issues in the template.
	page := strings.NewReplacer(
		"{{TOKEN}}", html.EscapeString(connectToken),
		"{{RELAY}}", html.EscapeString(relayBase),
		"{{INSTID}}", instIDStr,
	).Replace(setupSuccessHTML)
	fmt.Fprint(w, page)
}

// ─────────────────────────────────────────────────────────────────────────────
// Installations list (JSON, for dashboard / CLI)
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleInstallations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	installations, err := h.store.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list installations"})
		return
	}
	type row struct {
		ID           int64  `json:"id"`
		AccountLogin string `json:"account_login"`
		AccountType  string `json:"account_type"`
		OpsEndpoint  string `json:"ops_endpoint"`
		Active       bool   `json:"active"`
		CreatedAt    string `json:"created_at"`
		UpdatedAt    string `json:"updated_at"`
	}
	out := make([]row, len(installations))
	for i, inst := range installations {
		out[i] = row{
			ID:           inst.ID,
			AccountLogin: inst.AccountLogin,
			AccountType:  inst.AccountType,
			OpsEndpoint:  inst.OpsEndpoint,
			Active:       inst.Active,
			CreatedAt:    inst.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    inst.UpdatedAt.Format(time.RFC3339),
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"installations": out})
}

// ─────────────────────────────────────────────────────────────────────────────
// WebSocket connect endpoint (/api/github-app/connect)
// ─────────────────────────────────────────────────────────────────────────────

// handleConnect authenticates the client's OpsIntelligence instance using its
// connect token and upgrades the HTTP request to a WebSocket. The relay then
// pushes GitHub events to the client over this connection in real time.
//
// Query params: ?installation_id=<id>&token=<connect_token>
func (h *Handler) handleConnect(w http.ResponseWriter, r *http.Request) {
	instIDStr := r.URL.Query().Get("installation_id")
	token := strings.TrimSpace(r.URL.Query().Get("token"))

	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil || instID == 0 {
		http.Error(w, "invalid installation_id", http.StatusBadRequest)
		return
	}
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	// Verify token matches the installation.
	ct, err := h.tokenStore.GetByToken(r.Context(), token)
	if err != nil || ct.InstallationID != instID {
		h.log.Warn("githubapp: connect attempt with invalid token",
			zap.Int64("installation_id", instID))
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	inst, err := h.store.Get(r.Context(), instID)
	if err != nil {
		http.Error(w, "installation not found", http.StatusNotFound)
		return
	}
	if !inst.Active {
		http.Error(w, "installation suspended", http.StatusForbidden)
		return
	}

	h.hub.Upgrade(w, r, &ConnectedClient{
		InstallationID: instID,
		AccountLogin:   inst.AccountLogin,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Signature verification
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) verifySignature(r *http.Request, body []byte) error {
	if h.cfg.WebhookSecret == "" {
		return nil // no secret configured → skip (useful for local testing)
	}
	sigHeader := strings.TrimSpace(r.Header.Get("X-Hub-Signature-256"))
	const prefix = "sha256="
	if !strings.HasPrefix(sigHeader, prefix) {
		return errors.New("missing or malformed X-Hub-Signature-256")
	}
	got, err := hex.DecodeString(sigHeader[len(prefix):])
	if err != nil {
		return fmt.Errorf("signature hex decode: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(h.cfg.WebhookSecret))
	mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return errors.New("signature mismatch")
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Payload helpers
// ─────────────────────────────────────────────────────────────────────────────

func extractInstallationID(payload map[string]interface{}) (int64, bool) {
	inst, ok := payload["installation"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	switch v := inst["id"].(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	}
	return 0, false
}

type accountInfo struct {
	login string
	typ   string
}

func extractAccount(payload map[string]interface{}) accountInfo {
	acc, _ := payload["account"].(map[string]interface{})
	if acc == nil {
		acc, _ = payload["installation"].(map[string]interface{})
		if acc != nil {
			acc, _ = acc["account"].(map[string]interface{})
		}
	}
	if acc == nil {
		return accountInfo{}
	}
	login, _ := acc["login"].(string)
	typ, _ := acc["type"].(string)
	return accountInfo{login: login, typ: typ}
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ─────────────────────────────────────────────────────────────────────────────
// Setup page HTML
// ─────────────────────────────────────────────────────────────────────────────

const setupPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>OpsIntelligence — GitHub App Setup</title>
<style>
  *{box-sizing:border-box}
  body{font-family:system-ui,sans-serif;background:#0d1117;color:#c9d1d9;margin:0;display:flex;align-items:center;justify-content:center;min-height:100vh;padding:1rem}
  .card{background:#161b22;border:1px solid #30363d;border-radius:8px;padding:2rem;max-width:560px;width:100%%}
  h1{font-size:1.25rem;margin:0 0 0.25rem}
  .sub{color:#8b949e;font-size:.875rem;margin-bottom:1.5rem}
  label{display:block;font-size:.875rem;margin-bottom:.25rem;color:#c9d1d9}
  input[type=url],input[type=password]{width:100%%;padding:.5rem .75rem;background:#0d1117;border:1px solid #30363d;border-radius:6px;color:#c9d1d9;font-size:.875rem;margin-bottom:1rem}
  input:focus{outline:none;border-color:#388bfd}
  .hint{font-size:.75rem;color:#8b949e;margin-top:-.75rem;margin-bottom:1rem}
  button{width:100%%;padding:.6rem 1rem;background:#238636;color:#fff;border:none;border-radius:6px;font-size:.875rem;cursor:pointer;margin-top:.5rem}
  button:hover{background:#2ea043}
  .badge{display:inline-block;background:#21262d;border:1px solid #30363d;border-radius:4px;padding:.1rem .4rem;font-family:monospace;font-size:.8rem;color:#8b949e}
  .tabs{display:flex;gap:.5rem;margin-bottom:1.5rem}
  .tab{flex:1;padding:.5rem;border:1px solid #30363d;border-radius:6px;background:#0d1117;color:#8b949e;cursor:pointer;font-size:.8rem;text-align:center}
  .tab.active{border-color:#388bfd;color:#c9d1d9;background:#1c2433}
  .section{display:none}.section.show{display:block}
  hr{border:none;border-top:1px solid #21262d;margin:1.5rem 0}
</style>
</head>
<body>
<div class="card">
  <h1>⚙️ OpsIntelligence GitHub App Setup</h1>
  <p class="sub">Configure how GitHub events reach <strong>%s</strong>. Choose a connection mode below.</p>

  <div class="tabs">
    <div class="tab active" onclick="switchTab('ws')">🔌 WebSocket (recommended for on-premise)</div>
    <div class="tab" onclick="switchTab('http')">🌐 Public HTTPS endpoint</div>
  </div>

  <form method="POST">
    <input type="hidden" name="installation_id" value="%s">
    <input type="hidden" name="mode" id="mode-input" value="websocket">

    <div class="section show" id="section-ws">
      <p class="hint" style="margin:0 0 1rem;font-size:.8rem;color:#c9d1d9">
        Your OpsIntelligence connects <strong>outbound</strong> to this relay — no public IP or firewall rule needed.
        After saving, you will receive a <strong>connect token</strong> to paste into your OpsIntelligence config.
      </p>
    </div>

    <div class="section" id="section-http">
      <label>Your OpsIntelligence Endpoint URL</label>
      <input type="url" name="ops_endpoint" placeholder="https://opi.your-company.internal" value="%s">
      <p class="hint">Must be reachable from this relay server over HTTPS.</p>
      <label>Webhook Secret <span class="badge">webhooks.adapters.github.secret</span></label>
      <input type="password" name="ops_webhook_secret" placeholder="your-webhook-secret-from-config">
      <p class="hint">The relay re-signs payloads with this secret so your instance can verify them.</p>
    </div>

    <button type="submit">Save &amp; Generate Connect Token</button>
  </form>

  <p class="sub" style="margin-top:1rem;font-size:.75rem">Installation ID: <span class="badge">%s</span></p>
</div>
<script>
function switchTab(mode){
  document.querySelectorAll('.tab').forEach((t,i)=>t.classList.toggle('active',i===(mode==='ws'?0:1)));
  document.getElementById('section-ws').classList.toggle('show',mode==='ws');
  document.getElementById('section-http').classList.toggle('show',mode==='http');
  document.getElementById('mode-input').value=mode==='ws'?'websocket':'http';
}
</script>
</body>
</html>`

// setupSuccessHTML uses {{TOKEN}}, {{RELAY}}, {{INSTID}} placeholders replaced
// via strings.NewReplacer — no fmt.Fprintf %s verbs.
const setupSuccessHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>OpsIntelligence — Setup Complete</title>
<style>
  *{box-sizing:border-box}
  body{font-family:system-ui,sans-serif;background:#0d1117;color:#c9d1d9;margin:0;display:flex;align-items:center;justify-content:center;min-height:100vh;padding:1rem}
  .card{background:#161b22;border:1px solid #30363d;border-radius:8px;padding:2rem;max-width:600px;width:100%}
  h1{font-size:1.25rem;color:#3fb950;margin:0 0 .5rem}
  h2{font-size:.9rem;color:#c9d1d9;margin:1.5rem 0 .5rem}
  p,li{color:#8b949e;font-size:.875rem;line-height:1.6}
  pre{background:#0d1117;border:1px solid #30363d;border-radius:6px;padding:1rem;font-size:.8rem;overflow-x:auto;color:#79c0ff;position:relative}
  .copy-btn{position:absolute;top:.5rem;right:.5rem;background:#21262d;border:1px solid #30363d;color:#c9d1d9;border-radius:4px;padding:.2rem .5rem;font-size:.7rem;cursor:pointer}
  .copy-btn:hover{background:#30363d}
  .token{font-family:monospace;background:#21262d;border:1px solid #30363d;border-radius:4px;padding:.5rem .75rem;font-size:.85rem;color:#e3b341;word-break:break-all;display:block;margin:.25rem 0 1rem}
  .step{display:flex;gap:.75rem;margin-bottom:.75rem;align-items:flex-start}
  .num{background:#238636;color:#fff;border-radius:50%;width:1.4rem;height:1.4rem;display:flex;align-items:center;justify-content:center;font-size:.75rem;flex-shrink:0;margin-top:.1rem}
</style>
</head>
<body>
<div class="card">
  <h1>✅ Setup complete</h1>
  <p>Your connect token has been generated. Add the config below to your self-hosted OpsIntelligence instance to connect it to this relay.</p>

  <h2>Connect Token</h2>
  <span class="token">{{TOKEN}}</span>

  <h2>Add to your <code>opsintelligence.yaml</code></h2>
  <pre id="yaml-snippet"><code>github_app_connector:
  enabled: true
  relay_url: "{{RELAY}}"
  installation_id: {{INSTID}}
  connect_token: "{{TOKEN}}"</code><button class="copy-btn" onclick="copyYAML()">Copy</button></pre>

  <h2>How it connects</h2>
  <div class="step"><div class="num">1</div><p>Paste the config above into your <code>opsintelligence.yaml</code> (on your server).</p></div>
  <div class="step"><div class="num">2</div><p>Restart: <code>opsintelligence start</code> — your instance dials <strong>outbound</strong> to this relay over WebSocket. No public IP or firewall rules needed.</p></div>
  <div class="step"><div class="num">3</div><p>GitHub events for your org are pushed to your instance in real time and processed entirely by your local OpsIntelligence agent.</p></div>

  <p style="margin-top:1.5rem;font-size:.75rem;color:#484f58">You can close this window. Return here to rotate the token.</p>
</div>
<script>
function copyYAML(){
  navigator.clipboard.writeText(document.getElementById('yaml-snippet').querySelector('code').innerText);
}
</script>
</body>
</html>`
