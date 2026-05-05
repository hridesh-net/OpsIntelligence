package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"tailscale.com/tsnet"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/automation"
	authpkg "github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/channels"
	"github.com/opsintelligence/opsintelligence/internal/channels/msteams"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/memory"
	"github.com/opsintelligence/opsintelligence/internal/observability/correlation"
	"github.com/opsintelligence/opsintelligence/internal/observability/metrics"
	obstracing "github.com/opsintelligence/opsintelligence/internal/observability/tracing"
	"github.com/opsintelligence/opsintelligence/internal/webhookadapter"
	"github.com/opsintelligence/opsintelligence/internal/webui"
	"github.com/opsintelligence/opsintelligence/internal/webui/dashboard"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Gateway handles local/remote auth via Bearer token
	},
}

// Server represents the Gateway HTTP and WebSocket server.
type Server struct {
	Hub        *Hub
	HTTPServer *http.Server
	Port       int
	Bind       string
	Token      string // Bearer token for auth (empty = no auth)
	Runner     *agent.Runner
	Version    string
	Tailscale  struct {
		Mode        string
		ResetOnExit bool
	}
	TS *tsnet.Server
	// hostFunnel* is set when gateway.tailscale.mode=funnel with loopback/LAN bind
	// and we spawn `tailscale funnel <port>` (long-running child).
	hostFunnelMu    sync.Mutex
	hostFunnelCmd  *exec.Cmd
	hostFunnelPort int
	Config *config.Config
	Gmail  *automation.GmailWatcher
	Logger *zap.Logger

	// PRReview is the optional PR review pool monitor. When set, the gateway
	// mounts GET /api/v1/pr-reviews, GET /api/v1/pr-reviews/{id}/events,
	// and POST /api/v1/pr-reviews/{id}/cancel.
	PRReview *prReviewAdapter

	// RepoIntel is the optional Repo Intelligence adapter. When set, the
	// gateway mounts /api/v1/repos/* endpoints for repo management, memory
	// inspection, scan results, and user management.
	RepoIntel *RepoIntelAdapter

	// WebhookAdapters is the typed, pluggable webhook-adapter registry
	// (see internal/webhookadapter). When non-nil and non-empty the
	// gateway mounts its Router under /api/webhook/ and uses the shared
	// WebhookRunner for every accepted delivery. Populate this from
	// cmd/opsintelligence before Start().
	WebhookAdapters *webhookadapter.Registry
	// WebhookRunner is the backgrounded agent runner called for every
	// accepted webhook delivery. When nil, the gateway falls back to
	// driving s.Runner directly (one master run per delivery). Callers
	// that want to fan-out via sub-agents can supply their own.
	WebhookRunner webhookadapter.RunnerFn

	// AuthService, when non-nil, enables phase 2 identity endpoints
	// (/api/v1/auth/*, /api/v1/whoami) and the dashboard shell under
	// /dashboard/. Zero value keeps the legacy shared-Bearer-token
	// behaviour intact so existing deployments keep working.
	AuthService *AuthService

	// A2ATasks is the task store for the A2A protocol.
	// Initialised by NewServer; tracks running/completed tasks so peer agents
	// can poll tasks/get and cancel via tasks/cancel.
	A2ATasks *A2ATaskStore

	// TeamsChannel, when set, mounts the Microsoft Teams Bot Framework webhook
	// handler on this gateway at /teams/ (health at /teams/health, webhook at
	// /teams/api/messages). Set by main when channels.teams.expose_via: gateway.
	// The gateway's public URL (Tailscale Funnel, TLS, LAN) is inherited automatically.
	TeamsChannel        *msteams.Channel
	TeamsMessageHandler channels.MessageHandler
}

// NewServer initializes a new Gateway server on the specified port.
// maxWebSocketClients is passed to the hub (0 = unlimited concurrent WS clients).
func NewServer(port, maxWebSocketClients int) *Server {
	return &Server{
		Hub:      NewHub(maxWebSocketClients),
		Port:     port,
		A2ATasks: newA2ATaskStore(),
	}
}

func (s *Server) logger() *zap.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return zap.NewNop()
}

func (s *Server) withCorrelation(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := correlation.EnrichFromHTTPHeaders(r.Context(), r.Header)
		ctx, requestID := correlation.EnsureRequestID(ctx)
		r = r.WithContext(ctx)

		logFields := append(correlation.Fields(ctx),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("remote_addr", r.RemoteAddr),
		)
		s.logger().Info("gateway inbound request", logFields...)
		w.Header().Set(correlation.HeaderRequestID, requestID)
		h(w, r)
	}
}

// Start begins listening on the configured port.
func (s *Server) Start() error {
	go s.Hub.Run()
	metrics.Default().SetGatewayUp(true)

	// Start automation workers if configured
	if s.Config != nil && s.Config.Gmail.Enabled {
		// NewGmailWatcher expects (config, logger) - we might need to pass the logger to Server
		// For now, let's assume we can use a basic logger or pass it in later.
		// Actually, let's just initialize it in main.go and set it on the Server.
		if s.Gmail != nil {
			if err := s.Gmail.Start(context.Background()); err != nil {
				log.Printf("gmail: failed to start watcher: %v", err)
			}
		}
	}

	mux := http.NewServeMux()

	// ── Agent-built static dashboards (served under /workspace/) ───────────
	// Serves ~/.opsintelligence/workspace/public at /workspace/ — no Bearer token so
	// browsers can open links directly. Do not put secrets in this directory.
	if s.Config != nil {
		publicDir := filepath.Join(s.Config.StateDir, "workspace", "public")
		if err := os.MkdirAll(publicDir, 0o755); err != nil {
			log.Printf("gateway: workspace/public: %v", err)
		} else {
			mux.Handle("/workspace/", http.StripPrefix("/workspace/", http.FileServer(http.Dir(publicDir))))
		}
	}

	// ── Auth middleware wrapper ───────────────────────────────────────────────
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		if s.Token == "" {
			return h
		}
		return func(w http.ResponseWriter, r *http.Request) {
			tok := r.Header.Get("Authorization")
			if !strings.HasPrefix(tok, "Bearer ") || strings.TrimPrefix(tok, "Bearer ") != s.Token {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}

	// phase2OrLegacyAuth uses cookie sessions + API keys when AuthService is
	// wired (dashboard); otherwise the legacy gateway Bearer gate. GET/HEAD
	// skip CSRF; mutating verbs require CSRF for browser sessions (same as
	// /api/v1/config and /api/v1/users).
	//
	// CLI / machine callers that present the static gateway Bearer token
	// (s.Token) are accepted even when AuthService is wired: they are
	// not browser sessions so CSRF does not apply. A system principal is
	// injected so downstream RBAC and audit still see an identity.
	phase2OrLegacyAuth := func(h http.HandlerFunc) http.Handler {
		core := s.withCorrelation(h)
		if s.AuthService != nil {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if s.Token != "" {
					if tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); tok == s.Token {
						ctx := authpkg.WithPrincipal(r.Context(), authpkg.SystemPrincipal("gateway-cli"))
						core(w, r.WithContext(ctx))
						return
					}
				}
				if r.Method == http.MethodGet || r.Method == http.MethodHead {
					s.AuthService.Protect(http.HandlerFunc(core)).ServeHTTP(w, r)
					return
				}
				s.AuthService.ProtectCSRF(http.HandlerFunc(core)).ServeHTTP(w, r)
			})
		}
		return auth(core)
	}

	// ── Phase-2 auth endpoints + dashboard shell ─────────────────────────────
	// When an AuthService is wired the gateway exposes:
	//   GET  /api/v1/auth/status
	//   POST /api/v1/auth/{login,logout,bootstrap}
	//   GET  /api/v1/whoami
	//   GET  /dashboard/* (login page + app frame + static assets)
	// These are served outside the legacy Bearer auth wrapper because
	// the phase-2 AuthService manages its own credential chain
	// (cookie session → API key → legacy shared token).
	if s.AuthService != nil {
		s.AuthService.Mount(mux)
		mux.Handle("/dashboard/", http.StripPrefix("/dashboard/", dashboard.Handler()))
	}

	// ── Static web UI ─────────────────────────────────────────────────────────
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(webui.Assets()))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// With phase-2 auth the operator UI lives under /dashboard/; send browsers
		// there from / so http://host:port/ is never an empty or confusing page.
		if s.AuthService != nil {
			http.Redirect(w, r, "/dashboard/", http.StatusFound)
			return
		}
		data, err := webui.Assets().Open("index.html")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		defer data.Close()
		rs, ok := data.(io.ReadSeeker)
		if !ok {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, "index.html", time.Time{}, rs)
	})

	// ── Health ────────────────────────────────────────────────────────────────
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// ── API: Status ───────────────────────────────────────────────────────────
	mux.HandleFunc("/api/status", auth(s.withCorrelation(s.handleStatus)))
	mux.HandleFunc("/metrics", auth(s.withCorrelation(s.handleMetrics)))

	// ── API: PR Review pool ───────────────────────────────────────────────────
	if s.PRReview != nil {
		mux.Handle("/api/v1/pr-reviews", phase2OrLegacyAuth(s.PRReview.handleList))
		mux.Handle("/api/v1/pr-reviews/", phase2OrLegacyAuth(s.PRReview.handleDetail))
	}

	// ── API: Repo Intelligence ────────────────────────────────────────────────
	if s.RepoIntel != nil {
		mux.Handle("/api/v1/repos", phase2OrLegacyAuth(s.RepoIntel.HandleRepos))
		mux.Handle("/api/v1/repos/", phase2OrLegacyAuth(s.RepoIntel.HandleRepos))
	}

	// ── API: Chat (SSE streaming) ─────────────────────────────────────────────
	mux.HandleFunc("/api/chat", auth(s.withCorrelation(s.handleChat)))

	// ── WebSocket (legacy / channel use) ─────────────────────────────────────
	mux.HandleFunc("/ws", s.withCorrelation(func(w http.ResponseWriter, r *http.Request) {
		serveWs(s.Hub, s.logger(), w, r)
	}))

	// ── A2A Protocol ──────────────────────────────────────────────────────────
	mux.HandleFunc("/.well-known/agent.json", s.handleAgentCard)
	mux.HandleFunc("/api/a2a", s.handleA2A)

	// ── Webhooks ──────────────────────────────────────────────────────────────
	// Typed adapter registry (GitHub today, GitLab/Bitbucket/… later)
	// takes precedence at /api/webhook/. Each adapter does its own
	// verification (HMAC, mTLS, …) so they bypass the generic Bearer
	// auth wrapper.
	//
	// Requests that don't match a registered adapter path fall through
	// to the legacy generic-mappings handler below, which keeps the
	// Bearer token gate (X-OpsIntelligence-Token).
	webhookRouter := s.buildWebhookRouter()
	if webhookRouter != nil {
		mux.HandleFunc("/api/webhook/", func(w http.ResponseWriter, r *http.Request) {
			// Try typed adapter first.
			suffix := strings.TrimPrefix(r.URL.Path, "/api/webhook/")
			suffix = strings.Trim(suffix, "/")
			if s.WebhookAdapters != nil && s.WebhookAdapters.Lookup(suffix) != nil {
				s.withCorrelation(webhookRouter.ServeHTTP)(w, r)
				return
			}
			// Fallback to generic mappings (Bearer-protected).
			auth(s.withCorrelation(s.handleWebhook))(w, r)
		})
	} else {
		mux.HandleFunc("/api/webhook/", auth(s.withCorrelation(s.handleWebhook)))
	}

	// ── Teams gateway mount (expose_via: gateway) ────────────────────────────
	// Mounts the Bot Framework webhook handler at /teams/ so Teams and GitHub
	// webhooks share the same public HTTPS endpoint (Tailscale Funnel, TLS cert,
	// or cloud LB). Teams webhook URL: <gateway-url>/teams/api/messages
	if s.TeamsChannel != nil && s.TeamsMessageHandler != nil {
		teamsCtx := context.Background()
		teamsHandler := s.TeamsChannel.GatewayHandler(teamsCtx, s.TeamsMessageHandler)
		mux.Handle("/teams/", http.StripPrefix("/teams", teamsHandler))
		log.Printf("channels/msteams: gateway-mounted at /teams/api/messages")
	}

	if s.Gmail != nil {
		if err := s.Gmail.Start(context.Background()); err != nil {
			log.Printf("Error starting Gmail watcher: %v", err)
		}
	}

	addr := fmt.Sprintf(":%d", s.Port)
	// Accept both "tailnet" (legacy) and "tailscale" (written by the onboarding wizard).
	if s.Bind == "tailnet" || s.Bind == "tailscale" {
		// Embedded tsnet creates its OWN Tailscale node (hostname "opsintelligence")
		// separate from the host machine's node. It needs TS_AUTHKEY to authenticate
		// non-interactively; without it ListenFunnel blocks indefinitely waiting for
		// browser auth. When TS_AUTHKEY is absent we fall back to the host Tailscale
		// Funnel path (same as bind:lan + tailscale.mode:funnel) so the user doesn't
		// need an extra auth key — their existing Tailscale login is used instead.
		hasAuthKey := strings.TrimSpace(os.Getenv("TS_AUTHKEY")) != ""
		if !hasAuthKey && s.Tailscale.Mode == "funnel" {
			log.Printf("gateway: TS_AUTHKEY not set — falling back to host Tailscale Funnel (embedded tsnet needs a separate auth key)")
			log.Printf("gateway: binding on 0.0.0.0%s, then running `tailscale funnel %d` via host Tailscale", addr, s.Port)
			fullAddr := fmt.Sprintf("0.0.0.0%s", addr)
			s.HTTPServer = &http.Server{
				Addr:    fullAddr,
				Handler: mux,
			}
			go s.startHostFunnel(s.Port)
			log.Printf("OpsIntelligence gateway + web UI listening on http://%s", fullAddr)
			return s.HTTPServer.ListenAndServe()
		}

		// ── Embedded tsnet path (TS_AUTHKEY required for non-interactive auth) ──
		tsHostname := "opsintelligence"
		s.TS = &tsnet.Server{
			Hostname: tsHostname,
		}

		var ln net.Listener
		var err error

		if s.Tailscale.Mode == "funnel" {
			ln, err = s.TS.ListenFunnel("tcp", addr)
		} else {
			ln, err = s.TS.Listen("tcp", addr)
		}

		if err != nil {
			return fmt.Errorf("tailscale listen error: %w", err)
		}

		// Resolve and log the actual public URL once the tailnet connection is up.
		// Tailscale may take a few seconds to authenticate, so retry until the
		// MagicDNS suffix is available (up to 60 s) before giving up.
		go func() {
			lc, err := s.TS.LocalClient()
			if err != nil {
				log.Printf("OpsIntelligence gateway: could not get Tailscale local client: %v", err)
				return
			}
			scheme := "http"
			if s.Tailscale.Mode == "funnel" {
				scheme = "https"
			}
			deadline := time.Now().Add(60 * time.Second)
			for time.Now().Before(deadline) {
				st, err := lc.Status(context.Background())
				if err == nil && st.CurrentTailnet != nil && st.CurrentTailnet.MagicDNSSuffix != "" {
					suffix := st.CurrentTailnet.MagicDNSSuffix
					publicURL := fmt.Sprintf("%s://%s.%s", scheme, tsHostname, suffix)
					log.Printf("OpsIntelligence gateway public URL (Tailscale %s): %s", s.Tailscale.Mode, publicURL)
					if s.Tailscale.Mode == "funnel" {
						log.Printf("  Bot Framework Teams webhook: %s/teams/api/messages", publicURL)
						log.Printf("  GitHub webhook:              %s/api/webhook/github", publicURL)
					}
					return
				}
				time.Sleep(2 * time.Second)
			}
			log.Printf("OpsIntelligence gateway: Tailscale %s mode active but MagicDNS suffix unavailable after 60s — check Tailscale auth", s.Tailscale.Mode)
		}()

		s.HTTPServer = &http.Server{Handler: mux}
		log.Printf("OpsIntelligence gateway + web UI listening via Tailscale (%s) on %s", s.Tailscale.Mode, addr)
		return s.HTTPServer.Serve(ln)
	}

	// Default loopback or LAN bind
	bindAddr := "127.0.0.1"
	if s.Bind == "lan" {
		bindAddr = "0.0.0.0"
	}
	fullAddr := fmt.Sprintf("%s%s", bindAddr, addr)

	s.HTTPServer = &http.Server{
		Addr:    fullAddr,
		Handler: mux,
	}

	// Host Tailscale Funnel: gateway binds locally and we run `tailscale funnel <port>`
	// so the host machine's Tailscale node exposes the port publicly over HTTPS.
	// This avoids the embedded tsnet node and separate auth key requirement.
	if s.Tailscale.Mode == "funnel" {
		go s.startHostFunnel(s.Port)
	}

	log.Printf("OpsIntelligence gateway + web UI listening on http://%s", fullAddr)
	return s.HTTPServer.ListenAndServe()
}

// Stop safely shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	log.Printf("Stopping gateway...")
	metrics.Default().SetGatewayUp(false)
	if s.TS != nil {
		s.TS.Close()
	}
	s.hostFunnelMu.Lock()
	if s.hostFunnelCmd != nil && s.hostFunnelCmd.Process != nil {
		_ = s.hostFunnelCmd.Process.Kill()
		s.hostFunnelCmd = nil
	}
	port := s.hostFunnelPort
	s.hostFunnelPort = 0
	s.hostFunnelMu.Unlock()
	if port > 0 && s.Tailscale.ResetOnExit {
		s.stopHostFunnel(port)
	}
	if s.HTTPServer != nil {
		err := s.HTTPServer.Shutdown(ctx)
		if s.Gmail != nil {
			s.Gmail.Stop()
		}
		return err
	}
	return nil
}

// startHostFunnel runs `tailscale funnel <port>` using the host machine's
// Tailscale installation, then logs the resulting public HTTPS URL. This is
// the non-embedded path: the gateway binds on loopback/LAN and the host
// Tailscale node exposes it to the internet.
func (s *Server) startHostFunnel(port int) {
	bin := resolveHostTailscaleBin()
	if bin == "" {
		log.Printf("gateway: tailscale.mode=funnel but Tailscale CLI not found — run `tailscale funnel %d` manually", port)
		return
	}

	portStr := fmt.Sprintf("%d", port)
	// `tailscale funnel <port>` is a long-running proxy; do not use CombinedOutput
	// or this goroutine would block forever and never record hostFunnelPort / URLs.
	cmd := exec.Command(bin, "funnel", portStr)
	cmd.Env = hostTailscaleEnv(os.Environ(), bin)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		log.Printf("gateway: `tailscale funnel %s` failed to start: %v", portStr, err)
		return
	}
	s.hostFunnelMu.Lock()
	s.hostFunnelCmd = cmd
	s.hostFunnelPort = port
	s.hostFunnelMu.Unlock()
	log.Printf("gateway: Tailscale Funnel child started on port %s (pid %d)", portStr, cmd.Process.Pid)
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("gateway: tailscale funnel exited: %v", err)
		}
	}()

	// Resolve and log the public URL from host Tailscale status.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stCmd := exec.CommandContext(ctx, bin, "status", "--json")
		stCmd.Env = hostTailscaleEnv(os.Environ(), bin)
		stOut, stErr := stCmd.Output()
		cancel()
		if stErr != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var st struct {
			Self struct {
				DNSName string `json:"DNSName"`
			} `json:"Self"`
		}
		if json.Unmarshal(stOut, &st) == nil && strings.TrimSpace(st.Self.DNSName) != "" {
			host := strings.TrimSuffix(strings.TrimSpace(st.Self.DNSName), ".")
			publicURL := fmt.Sprintf("https://%s", host)
			log.Printf("OpsIntelligence gateway public URL (host Tailscale Funnel): %s", publicURL)
			log.Printf("  Bot Framework Teams webhook: %s/teams/api/messages", publicURL)
			log.Printf("  GitHub webhook:              %s/api/webhook/github", publicURL)
			return
		}
		time.Sleep(2 * time.Second)
	}
	log.Printf("gateway: Tailscale Funnel started but could not resolve public hostname")
}

// stopHostFunnel tears down the host `tailscale funnel` on the given port.
func (s *Server) stopHostFunnel(port int) {
	bin := resolveHostTailscaleBin()
	if bin == "" {
		return
	}
	// "tailscale funnel --https=<port> off" or "tailscale funnel off" depending on version.
	// Try the port-specific form first; fall back to blanket off.
	portStr := fmt.Sprintf("%d", port)
	off1 := exec.Command(bin, "funnel", "--https="+portStr, "off")
	off1.Env = hostTailscaleEnv(os.Environ(), bin)
	if out, err := off1.CombinedOutput(); err != nil {
		log.Printf("gateway: `tailscale funnel --https=%s off` failed (%v: %s), trying `tailscale funnel off`", portStr, err, strings.TrimSpace(string(out)))
		off2 := exec.Command(bin, "funnel", "off")
		off2.Env = hostTailscaleEnv(os.Environ(), bin)
		if out2, err2 := off2.CombinedOutput(); err2 != nil {
			log.Printf("gateway: `tailscale funnel off` failed: %v — %s", err2, strings.TrimSpace(string(out2)))
		}
	}
}

// resolveHostTailscaleBin finds the host Tailscale CLI binary.
// Checks OPSINTELLIGENCE_TAILSCALE_BIN, TAILSCALE_CLI, PATH, then the
// macOS .app bundle location.
func resolveHostTailscaleBin() string {
	for _, env := range []string{"OPSINTELLIGENCE_TAILSCALE_BIN", "TAILSCALE_CLI"} {
		if p := strings.TrimSpace(os.Getenv(env)); p != "" {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	if p, err := exec.LookPath("tailscale"); err == nil && p != "" {
		return p
	}
	// macOS Tailscale.app ships the CLI inside the bundle but doesn't add it to PATH.
	const macAppCLI = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	if st, err := os.Stat(macAppCLI); err == nil && !st.IsDir() {
		return macAppCLI
	}
	return ""
}

func hostTailscaleEnv(base []string, bin string) []string {
	env := base
	if strings.Contains(bin, "Tailscale.app") {
		env = append(append([]string(nil), env...), "TAILSCALE_BE_CLI=1")
	}
	return env
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(metrics.Default().RenderPrometheus()))
}

// ── API Handlers ──────────────────────────────────────────────────────────────

// chatRequest is the JSON body for POST /api/chat.
type chatRequest struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id"`
}

// sseEvent formats an SSE data line.
func sseEvent(eventType, content string) []byte {
	payload, _ := json.Marshal(map[string]string{"type": eventType, "content": content})
	return []byte("data: " + string(payload) + "\n\n")
}

func sseDone() []byte { return []byte("data: [DONE]\n\n") }

func sseToolEvent(eventType, name string) []byte {
	payload, _ := json.Marshal(map[string]string{"type": eventType, "name": name})
	return []byte("data: " + string(payload) + "\n\n")
}

// handleChat handles POST /api/chat, returning an SSE stream of tokens.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Runner == nil {
		http.Error(w, `{"error":"agent not initialised"}`, http.StatusServiceUnavailable)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// Pick or create session-specific runner
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	sessionRunner := s.Runner.WithSession(sessionID)
	ctx := correlation.WithSessionID(r.Context(), sessionID)
	ctx = correlation.WithChannel(ctx, "gateway")
	spanCtx, span := obstracing.StartSpan(ctx, "gateway.receive_message")
	defer span.End()
	s.logger().Info("gateway chat request",
		append(correlation.Fields(spanCtx),
			zap.String("event", "chat.receive"),
		)...,
	)

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	done := make(chan struct{})

	handler := &sseStreamHandler{
		w:       w,
		flusher: flusher,
		done:    done,
	}

	go func() {
		sessionRunner.RunStream(spanCtx, memory.Message{
			ID:        uuid.New().String(),
			SessionID: sessionID,
			Role:      memory.RoleUser,
			Content:   req.Message,
			CreatedAt: time.Now(),
		}, handler)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// handleStatus handles GET /api/status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	model := ""
	if s.Runner != nil {
		// Runner doesn't expose model publicly, we embed it in Server
	}
	resp := map[string]interface{}{
		"status":  "ok",
		"version": s.Version,
		"pid":     os.Getpid(),
		"model":   model,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// handleWebhook handles incoming webhooks, mapping them to agent prompts.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if s.Config == nil || !s.Config.Webhooks.Enabled {
		http.Error(w, `{"error":"webhooks disabled"}`, http.StatusForbidden)
		return
	}

	// Check for optional webhook token if configured
	if s.Config.Webhooks.Token != "" {
		tok := r.Header.Get("X-OpsIntelligence-Token")
		if tok != s.Config.Webhooks.Token {
			http.Error(w, `{"error":"invalid webhook token"}`, http.StatusUnauthorized)
			return
		}
	}

	// Get the preset path from the URL
	path := strings.TrimPrefix(r.URL.Path, "/api/webhook/")
	if path == "" {
		http.Error(w, `{"error":"invalid webhook path"}`, http.StatusBadRequest)
		return
	}

	// Find the mapping for this path
	var mapping *config.WebhookMapping
	for _, m := range s.Config.Webhooks.Mappings {
		if m.Path == path {
			mapping = &m
			break
		}
	}

	if mapping == nil {
		http.Error(w, `{"error":"webhook mapping not found"}`, http.StatusNotFound)
		return
	}

	// Read and parse the payload
	var payload map[string]interface{}
	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, `{"error":"failed to decode json"}`, http.StatusBadRequest)
			return
		}
	}

	// Execute via agent
	prompt := mapping.PromptTemplate
	// Simple template replacement
	for k, v := range payload {
		placeholder := fmt.Sprintf("{{.%s}}", k)
		prompt = strings.ReplaceAll(prompt, placeholder, fmt.Sprintf("%v", v))
	}

	ctx := correlation.WithChannel(r.Context(), "webhook")
	s.logger().Info("webhook receive",
		append(correlation.Fields(ctx), zap.String("path", path))...,
	)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if s.Runner == nil {
		http.Error(w, `{"error":"agent not initialised"}`, http.StatusServiceUnavailable)
		return
	}

	sessionID := "webhook:" + path + ":" + uuid.New().String()
	ctx = correlation.WithSessionID(ctx, sessionID)
	runner := s.Runner.WithSession(sessionID)

	res, err := runner.Run(ctx, memory.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      memory.RoleUser,
		Content:   prompt,
		CreatedAt: time.Now(),
	})

	if err != nil {
		s.logger().Error("webhook agent failed",
			append(correlation.Fields(ctx), zap.Error(err))...,
		)
		http.Error(w, fmt.Sprintf(`{"error":"agent execution failed","details":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{
		"status":     "success",
		"iterations": res.Iterations,
	}

	// If delivery is requested, the agent's response is already in memory/last message
	// The implementation of 'deliver: true' usually involves the agent itself calling
	// a message tool, but if we want it automatic, we'd trigger it here.
	// For now, we return 200 OK.

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// buildWebhookRouter wires a webhookadapter.Router when any adapter is
// registered. Returns nil if the registry is empty/disabled, in which
// case the gateway serves only the legacy generic mappings handler.
func (s *Server) buildWebhookRouter() *webhookadapter.Router {
	if s.Config == nil || !s.Config.Webhooks.Enabled {
		return nil
	}
	if s.WebhookAdapters == nil || len(s.WebhookAdapters.List()) == 0 {
		return nil
	}
	runner := s.WebhookRunner
	if runner == nil {
		runner = defaultWebhookRunner(s.Runner, s.logger())
	}
	return &webhookadapter.Router{
		Registry:      s.WebhookAdapters,
		Runner:        runner,
		Log:           s.logger(),
		Timeout:       parseDurationOr(s.Config.Webhooks.Timeout, 10*time.Minute),
		MaxConcurrent: s.Config.Webhooks.MaxConcurrent,
	}
}

// defaultWebhookRunner drives the master agent runner for every accepted
// webhook delivery. Callers that want to fan out per-event work to
// sub-agents can supply their own RunnerFn on Server.WebhookRunner.
func defaultWebhookRunner(runner *agent.Runner, log *zap.Logger) webhookadapter.RunnerFn {
	if runner == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return func(ctx context.Context, e webhookadapter.Event, prompt string) {
		ctx = correlation.WithChannel(ctx, "webhook:"+e.Source)
		ctx = correlation.WithSessionID(ctx, e.SessionID)
		session := runner.WithSession(e.SessionID)
		_, err := session.Run(ctx, memory.Message{
			ID:        uuid.New().String(),
			SessionID: e.SessionID,
			Role:      memory.RoleUser,
			Content:   prompt,
			CreatedAt: time.Now(),
		})
		if err != nil {
			log.Error("webhook agent run failed",
				zap.String("adapter", e.Source),
				zap.String("kind", e.Kind),
				zap.String("delivery_id", e.DeliveryID),
				zap.String("session_id", e.SessionID),
				zap.Error(err))
			return
		}
		log.Info("webhook agent run completed",
			zap.String("adapter", e.Source),
			zap.String("kind", e.Kind),
			zap.String("delivery_id", e.DeliveryID),
			zap.String("session_id", e.SessionID))
	}
}

func parseDurationOr(s string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// ── SSE Stream Handler ────────────────────────────────────────────────────────

type sseStreamHandler struct {
	w       http.ResponseWriter
	flusher http.Flusher
	done    chan struct{}
}

func (h *sseStreamHandler) write(b []byte) {
	_, _ = h.w.Write(b)
	h.flusher.Flush()
}

func (h *sseStreamHandler) OnToken(token string) {
	h.write(sseEvent("token", token))
}

func (h *sseStreamHandler) OnToolCall(name string, _ json.RawMessage) {
	h.write(sseToolEvent("tool_start", name))
}

func (h *sseStreamHandler) OnToolResult(name string, _ string) {
	h.write(sseToolEvent("tool_end", name))
}

func (h *sseStreamHandler) OnDone(_ *agent.RunResult) {
	h.write(sseDone())
	close(h.done)
}

func (h *sseStreamHandler) OnError(err error) {
	h.write(sseEvent("error", err.Error()))
	h.write(sseDone())
	select {
	case <-h.done:
	default:
		close(h.done)
	}
}

// ── WebSocket (unchanged from original) ──────────────────────────────────────

func serveWs(hub *Hub, logger *zap.Logger, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("gateway/server upgrade error:", err)
		return
	}

	clientID := uuid.New().String()
	client := &Client{
		ID:   clientID,
		Hub:  hub,
		Conn: conn,
		Send: make(chan []byte, 256),
	}

	if logger != nil {
		logger.Info("gateway websocket connected",
			append(correlation.Fields(r.Context()), zap.String("client_id", clientID))...,
		)
	}

	ok := make(chan bool, 1)
	hub.register <- registerOp{client: client, ok: ok}
	if !<-ok {
		if logger != nil {
			logger.Warn("gateway websocket rejected (client cap)",
				append(correlation.Fields(r.Context()),
					zap.String("client_id", clientID),
					zap.Int("max_websocket_clients", hub.MaxWSClients),
				)...,
			)
		}
		deadline := time.Now().Add(writeWait)
		msg := websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "gateway: max websocket clients")
		_ = conn.WriteControl(websocket.CloseMessage, msg, deadline)
		_ = conn.Close()
		return
	}

	go client.writePump()
	go client.readPump()
}
