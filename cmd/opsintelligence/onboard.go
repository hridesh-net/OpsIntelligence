package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/cmd/opsintelligence/tui"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/tuibridge"
	"github.com/spf13/cobra"
)

type provEntry struct {
	provider     string
	apiKey       string
	baseURL      string
	apiVersion   string
	awsRegion    string
	awsProfile   string
	awsAccessKey string
	awsSecretKey string
	vertexProj   string
	vertexLoc    string
	vertexCreds  string
	model        string
}

type embedEntry struct {
	provider string
	apiKey   string
	baseURL  string
	model    string
}

func inferBedrockAuthMode(e provEntry) string {
	if strings.TrimSpace(e.apiKey) != "" {
		return "api_key"
	}
	if strings.TrimSpace(e.awsProfile) != "" {
		return "profile"
	}
	if strings.TrimSpace(e.awsAccessKey) != "" {
		return "iam"
	}
	return "profile"
}


const tailscaleMacAppCLI = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"

// resolveTailscaleCLI returns an executable path for the Tailscale CLI.
// Order: OPSINTELLIGENCE_TAILSCALE_BIN, TAILSCALE_CLI, PATH lookup,
// then the binary bundled inside Tailscale.app on macOS (not on PATH by default).
func resolveTailscaleCLI() string {
	for _, key := range []string{"OPSINTELLIGENCE_TAILSCALE_BIN", "TAILSCALE_CLI"} {
		if p := strings.TrimSpace(os.Getenv(key)); p != "" {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	if p, err := exec.LookPath("tailscale"); err == nil && p != "" {
		return p
	}
	if runtime.GOOS == "darwin" {
		if st, err := os.Stat(tailscaleMacAppCLI); err == nil && !st.IsDir() {
			return tailscaleMacAppCLI
		}
	}
	return ""
}

// tailscaleStatusJSON runs `tailscale status --json` via resolveTailscaleCLI.
func tailscaleStatusJSON(ctx context.Context) ([]byte, error) {
	bin := resolveTailscaleCLI()
	if bin == "" {
		return nil, fmt.Errorf("tailscale CLI not found: install CLI integration from Tailscale settings, add brew's tailscale to PATH, or set OPSINTELLIGENCE_TAILSCALE_BIN")
	}
	cmd := exec.CommandContext(ctx, bin, "status", "--json")
	if runtime.GOOS == "darwin" && strings.Contains(bin, "Tailscale.app") {
		// Bundled macOS binary chooses GUI vs CLI from env; force CLI when spawned from OpsIntelligence.
		cmd.Env = append(os.Environ(), "TAILSCALE_BE_CLI=1")
	}
	return cmd.Output()
}

// detectTailscaleHostname queries `tailscale status --json` and returns the
// machine's Tailscale FQDN (e.g. "mymachine.tail1234.ts.net"). Returns "" if
// Tailscale is not installed, not running, or the query fails.
func detectTailscaleHostname() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := tailscaleStatusJSON(ctx)
	if err != nil {
		return ""
	}
	var st struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return ""
	}
	// DNSName has a trailing dot — trim it.
	return strings.TrimSuffix(strings.TrimSpace(st.Self.DNSName), ".")
}

// placeholderGatewayHost reports values that are fine for loopback/LAN gateway
// host but must not be used as a Tailscale MagicDNS / Funnel public hostname.
func placeholderGatewayHost(h string) bool {
	switch strings.ToLower(strings.TrimSpace(h)) {
	case "", "127.0.0.1", "localhost", "::1", "0.0.0.0":
		return true
	default:
		return false
	}
}

// embeddedTsnetDNSLabel must match the Hostname passed to tsnet.Server in
// internal/gateway/server.go for the embedded gateway listener.
const embeddedTsnetDNSLabel = "opsintelligence"

// embeddedTailscaleGatewayOrigin returns the origin for the embedded tsnet
// listener (http://opsintelligence.<magic-dns-suffix> for serve, https:// for
// funnel). It returns "" if the Tailscale CLI is unavailable or MagicDNS
// suffix cannot be read.
func embeddedTailscaleGatewayOrigin(bind, tailscaleMode string) string {
	b := strings.TrimSpace(bind)
	if b != "tailscale" && b != "tailnet" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := tailscaleStatusJSON(ctx)
	if err != nil {
		return ""
	}
	var st struct {
		CurrentTailnet struct {
			MagicDNSSuffix string `json:"MagicDNSSuffix"`
		} `json:"CurrentTailnet"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return ""
	}
	suffix := strings.TrimSuffix(strings.TrimSpace(st.CurrentTailnet.MagicDNSSuffix), ".")
	if suffix == "" {
		return ""
	}
	scheme := "http"
	if strings.EqualFold(strings.TrimSpace(tailscaleMode), "funnel") {
		scheme = "https"
	}
	return scheme + "://" + embeddedTsnetDNSLabel + "." + suffix
}

// effectiveGatewayOrigin returns the best gateway URL origin for the CLI and
// status dashboard (health, /dashboard/, API callers). When gateway.bind is
// embedded Tailscale, this prefers https://opsintelligence.<tailnet-suffix>
// over gateway.host from YAML (which names this OS machine, not the tsnet
// listener hostname).
func effectiveGatewayOrigin(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	bind := strings.TrimSpace(cfg.Gateway.Bind)
	if bind == "tailscale" || bind == "tailnet" {
		if o := embeddedTailscaleGatewayOrigin(bind, cfg.Gateway.Tailscale.Mode); o != "" {
			return o
		}
	}
	return cfg.PublicGatewayBaseURL()
}

func normalizeGatewayBind(bind string) string {
	switch strings.TrimSpace(bind) {
	case "loopback", "127.0.0.1", "":
		return "loopback"
	case "lan", "0.0.0.0":
		return "lan"
	case "tailscale", "tailnet":
		return "tailscale"
	default:
		return bind
	}
}

func onboardCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "onboard",
		Short: "Run the interactive first-time setup wizard",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := gf.configPath
			if path == "" {
				path = config.DefaultConfigPath()
			}
			shouldStart, err := runOnboarding(path)
			if err != nil {
				return err
			}
			if shouldStart {
				fmt.Println("\n🚀 Starting OpsIntelligence in background mode...")
				return Detach("start")
			}
			return nil
		},
	}
}

// openAICompatProviders is the list of provider keys that work with Plano
// (all OpenAI-compatible providers). Anthropic, Bedrock, and Vertex use their
// own proprietary protocols and are NOT OpenAI-compatible.
var openAICompatProviders = map[string]bool{
	"openai":     true,
	"azure":      true,
	"ollama":     true,
	"vllm":       true,
	"lmstudio":   true,
	"gemini":     true,
	"groq":       true,
	"mistral":    true,
	"openrouter": true,
	"deepseek":   true,
	"perplexity": true,
	"nvidia":     true,
	"xai":        true,
}


func runOnboarding(configPath string) (bool, error) {
	// The Rust wizard renders its own header + subtitle; we deliberately
	// don't print a Go-side splash banner first so the user sees a single
	// alt-screen TUI instead of two stacked headers.

	// Load existing config if available to pre-populate defaults
	var existing *config.Config
	if _, err := os.Stat(configPath); err == nil {
		if c, err := config.Load(configPath); err == nil {
			existing = c
		}
	}

	steps, state := BuildOnboardStepsWizard(configPath, existing)
	if err := tuibridge.RunWizard(context.Background(), tuibridge.WizardOptions{
		Brand: "OPSINTELLIGENCE",
		Steps: steps,
	}); err != nil {
		return false, err
	}
	if !state.done {
		// Wizard was aborted by the user.
		return false, nil
	}

	tui.PrintOnboardSaved(configPath)

	// Build the Tailscale public URL for the summary when Funnel is configured.
	// Works for both embedded tsnet (bind: tailscale) and host Tailscale Funnel
	// (bind: loopback/lan + tailscale.mode: funnel).
	var tailscalePublicURL string
	if strings.EqualFold(state.tsMode, "funnel") && !placeholderGatewayHost(state.gwHost) {
		tailscalePublicURL = "https://" + strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(state.gwHost), "https://"), "http://")
	}

	// Print a compact summary; legacy bubbletea tabbed summary was removed in
	// Phase 5d. Run `opsintelligence status` for a live tabbed view.
	printOnboardSummary(state, tailscalePublicURL)

	// When Tailscale Funnel is configured, print the webhook URLs the user needs
	// to register in Azure / GitHub so they can copy them directly.
	if tailscalePublicURL != "" {
		fmt.Println("─────────────────────────────────────────────")
		fmt.Println("  Webhook URLs (register these in your portals)")
		fmt.Println()
		if len(state.selectedChannels) > 0 {
			for _, ch := range state.selectedChannels {
				if ch == "msteams" {
					fmt.Printf("  Microsoft Teams  →  %s/teams/api/messages\n", tailscalePublicURL)
				}
			}
		}
		fmt.Printf("  GitHub webhook   →  %s/api/webhook/github\n", tailscalePublicURL)
		fmt.Println()
		fmt.Println("  Set these in Azure Bot → Configuration and GitHub → Settings → Webhooks.")
		fmt.Println("─────────────────────────────────────────────")
		fmt.Println()
	}

	return true, nil
}


func buildChannelSetupGuideLines(selectedChannels []string, gwHost string, gwPort int) []string {
	if len(selectedChannels) == 0 {
		return nil
	}
	enabled := make(map[string]bool, len(selectedChannels))
	for _, ch := range selectedChannels {
		enabled[strings.ToLower(strings.TrimSpace(ch))] = true
	}

	var out []string
	if enabled["telegram"] {
		out = append(out,
			"[Telegram] In @BotFather run /setprivacy -> Disable if you want all group messages.",
			"[Telegram] Keep privacy enabled for mention-only group behavior (recommended).",
			"[Telegram] DM the bot once, then use /status to verify replies.",
		)
	}
	if enabled["discord"] {
		out = append(out,
			"[Discord] Enable Message Content Intent in the Discord Developer Portal.",
			"[Discord] Invite bot with View Channels, Send Messages, Read Message History, Add Reactions.",
			"[Discord] If require_mention=true, use @BotName in guild channels to trigger replies.",
		)
	}
	if enabled["slack"] {
		out = append(out,
			"[Slack] Install app to workspace and confirm Bot + App tokens are valid.",
			"[Slack] Enable Socket Mode and subscribe to app_mention + message.im events.",
		)
	}
	if enabled["whatsapp"] {
		out = append(out,
			"[WhatsApp] Run `opsintelligence start` and scan QR (Linked Devices) if not already linked.",
		)
	}

	out = append(out,
		"[All channels] Run `opsintelligence doctor` to validate config and channel tokens (doc/runbooks/doctor-config-validation.md).",
		"[Docs] Telegram setup: doc/channels/telegram-setup.md",
		"[Docs] Discord setup: doc/channels/discord-setup.md",
		fmt.Sprintf("[Web UI] http://%s:%d", gwHost, gwPort),
	)
	return out
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// setupPlanoDocker checks Docker availability, pulls the Plano image, and
// starts a detached Plano container listening on port 12000.
// Returns true if Plano is confirmed reachable after setup.
// setupPlanoDocker is called inside a RunWithSpinner, so it suppresses intermediate output.
// Returns true when Plano is reachable after setup.
func setupPlanoDocker(endpoint string) bool {
	// 1. Check docker binary
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		return false
	}

	// 2. Check if plano container already running
	out, _ := exec.Command("docker", "ps", "--filter", "name=plano", "--format", "{{.Names}}").Output()
	if strings.Contains(string(out), "plano") {
		return waitForPlano(endpoint, 5)
	}

	// 3. Pull image (suppress output — spinner is showing)
	pullCmd := exec.Command("docker", "pull", "katanemo/plano:latest")
	pullCmd.Stdout = nil
	pullCmd.Stderr = nil
	if err := pullCmd.Run(); err != nil {
		return false
	}

	// 4. Remove any stopped plano container
	_ = exec.Command("docker", "rm", "-f", "plano").Run()

	// 5. Start container detached
	startCmd := exec.Command("docker", "run", "-d",
		"--name", "plano",
		"-p", "12000:12000",
		"--restart", "unless-stopped",
		"katanemo/plano:latest",
	)
	if _, err := startCmd.CombinedOutput(); err != nil {
		return false
	}

	// 6. Wait for readiness
	return waitForPlano(endpoint, 15)
}

// waitForPlano polls the Plano /v1/models endpoint until it responds or timeout expires.
func waitForPlano(endpoint string, timeoutSec int) bool {
	modelsURL := strings.TrimRight(endpoint, "/") + "/models"
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := client.Get(modelsURL); err == nil && resp.StatusCode < 400 {
			resp.Body.Close()
			return true
		}
		fmt.Print(".")
		time.Sleep(1 * time.Second)
	}
	return false
}
