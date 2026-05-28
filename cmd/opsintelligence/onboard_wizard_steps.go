package main

// onboard_wizard_steps.go — parallel onboarding step builders that target the
// Rust form engine (tuibridge.RunWizard) instead of huh forms.
//
// This file is the Phase 5c incremental port of cmd/opsintelligence/onboard_steps.go.
// Each function below returns a []tuibridge.WizardStep covering one sub-flow.
// The corresponding huh-based code in onboard_steps.go remains the authoritative
// path until full parity is reached; nothing in this file is wired into the
// production onboarding flow yet. The hidden `opsintelligence tui-onboard-preview`
// command exercises everything ported here so the round-trip can be validated.
//
// Porting pattern:
//
//   OLD (huh)                                NEW (tuibridge)
//   ─────────────────────────────────────    ──────────────────────────────────
//   huh.NewSelect[string]().                 tuibridge.WizardSelect(key, label,
//     Title("…").                              description, defaultVal, options)
//     Description("…").                      → field map["key"] is a string
//     Options(…).
//     Value(&s.field)                        OnSubmit assigns to s.field
//                                            via tuibridge.WizardString(...)
//
//   huh.NewInput().Password(true)            tuibridge.WizardPassword(key, …)
//   huh.NewInput()                           tuibridge.WizardInput(key, …)
//   huh.NewConfirm().Value(&s.b)             tuibridge.WizardConfirm(key, …)
//   huh.NewMultiSelect[string]()             tuibridge.WizardMultiSelect(key, …)
//   huh.NewNote().Title()                    tuibridge.WizardNote(title, desc)
//
//   tui.OnboardConditionalFormStep(…cond…)   WizardStep.Skip: func() bool { !cond() }
//   tui.OnboardSideStep(label, fn)           WizardStep{SideEffectLabel, SideEffect: fn}

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/localintel"
	"github.com/opsintelligence/opsintelligence/internal/provider/bedrock"
	"github.com/opsintelligence/opsintelligence/internal/skills"
	"github.com/opsintelligence/opsintelligence/internal/tuibridge"
)

// channelModeOptionsWizard mirrors channelModeOptions() from onboard_steps.go.
func channelModeOptionsWizard() []tuibridge.WizardOptionSpec {
	return []tuibridge.WizardOptionSpec{
		{Value: "pairing", Label: "Pairing (Recommended)"},
		{Value: "allowlist", Label: "Allowlist only"},
		{Value: "open", Label: "Open (Public)"},
		{Value: "disabled", Label: "Disabled"},
	}
}

// BuildOnboardStepsWizard builds the Phase-5c subset of the onboarding wizard:
//   * Secondary provider gate
//   * Plano smart routing (confirm + config + docker side-effect)
//   * Model routing (coding + vision)
//   * Embeddings (provider + credentials)
//
// The primary-provider step (which historically uses providerSteps for every
// provider) is represented by a single simplified select-only step so the
// preview can be driven end-to-end. Full provider-credentials handling for
// every supported provider is part of Phase 5c chunk 2 (not in this commit).
func BuildOnboardStepsWizard(configPath string, existing *config.Config) ([]tuibridge.WizardStep, *onboardState) {
	s := &onboardState{
		configPath:       configPath,
		existing:         existing,
		gwMode:           "",
		gwPort:           18790,
		gwHost:           "127.0.0.1",
		tsMode:           "off",
		secChoice:        "none",
		tgDMMode:         "pairing",
		dcDMMode:         "pairing",
		dcRequireMention: true,
		slDMMode:         "pairing",
		waDMMode:         "pairing",
		teamsDMMode:      "allowlist",
		teamsListenAddr:  ":3978",
	}
	if existing != nil {
		populateFromExisting(s, existing)
	}

	var steps []tuibridge.WizardStep

	// Per-provider auth mode strings (kept by closure capture, matches the
	// behaviour of providerSteps in onboard_steps.go which uses pointer-bound
	// locals for the same purpose).
	var bedrockAuthPrimary, bedrockAuthSecondary string

	// ── 1. Primary provider (full provider sub-flow) ────────────────────────
	steps = append(steps, providerStepsWizard(
		"🧠", "AI Provider", "Select the primary LLM that powers your agent",
		&s.primary, true, &bedrockAuthPrimary, nil,
	)...)

	// ── 2. Secondary provider gate ──────────────────────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:     "🔀",
		Title:    "Secondary Provider",
		Subtitle: "Optional fallback or high-availability provider",
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardSelect("sec_choice", "Secondary / Fallback Provider?",
						"Pick a second model for high availability or specific tasks.",
						s.secChoice,
						[]tuibridge.WizardOptionSpec{
							{Value: "none", Label: "None"},
							{Value: "configure", Label: "Choose a secondary provider"},
						}),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.secChoice = tuibridge.WizardString(f, "sec_choice", "none")
			return nil
		},
	})

	// Secondary provider full sub-flow (only if user picked "configure").
	steps = append(steps, providerStepsWizard(
		"🔀", "Secondary Provider", "",
		&s.secondary, false, &bedrockAuthSecondary,
		func() bool { return s.secChoice == "configure" },
	)...)

	// ── 3. Plano smart routing — enable confirm ─────────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:     "⚡",
		Title:    "Smart Routing",
		Subtitle: "Auto-route prompts by complexity to save 30–60% on LLM costs",
		Skip: func() bool {
			return !(s.secChoice == "configure" && s.secondary.provider != "" && s.secondary.provider != "none")
		},
		Form: func() tuibridge.WizardFormSpec {
			desc := "Requires Docker. Runs locally on port 12000."
			if !openAICompatProviders[s.primary.provider] {
				desc += fmt.Sprintf(
					"\n\n⚠ Note: your primary provider (%s) is not OpenAI-compatible.\nPlano will route to your secondary for complex tasks.",
					s.primary.provider)
			}
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardConfirm("use_plano",
						"Enable Smart Routing with Plano?",
						desc, s.usePlano, "Yes", "No"),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.usePlano = tuibridge.WizardBool(f, "use_plano", false)
			return nil
		},
	})

	// ── 4. Plano config ─────────────────────────────────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:  "⚡",
		Title: "Smart Routing — Plano Config",
		Skip:  func() bool { return !(s.secChoice == "configure" && s.usePlano) },
		Form: func() tuibridge.WizardFormSpec {
			if strings.TrimSpace(s.planoEndpoint) == "" {
				s.planoEndpoint = "http://localhost:12000/v1"
			}
			fastOpts := []tuibridge.WizardOptionSpec{
				{Value: "openai/gpt-4o-mini", Label: "GPT-4o mini"},
				{Value: "groq/llama3-8b-8192", Label: "Groq Llama3 8B"},
				{Value: "mistral/mistral-7b-instruct", Label: "Mistral 7B"},
				{Value: "ollama/llama3.2", Label: "Ollama Llama3.2"},
				{Value: "deepseek/deepseek-chat", Label: "DeepSeek V2 Lite"},
			}
			powerOpts := []tuibridge.WizardOptionSpec{
				{Value: "openai/gpt-4o", Label: "GPT-4o"},
				{Value: "openai/gpt-4.1", Label: "GPT-4.1"},
				{Value: "groq/llama3-70b-8192", Label: "Groq Llama3 70B"},
				{Value: "mistral/mistral-large-latest", Label: "Mistral Large"},
				{Value: "ollama/llama3.1:70b", Label: "Ollama Llama3.1 70B"},
				{Value: "deepseek/deepseek-r1", Label: "DeepSeek R1"},
			}
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardInput("endpoint", "Plano endpoint",
						"Leave as default if running Plano locally via Docker.",
						s.planoEndpoint),
					tuibridge.WizardSelect("fast_model", "Fast model — for simple queries",
						"", s.planoFastModel, fastOpts),
					tuibridge.WizardSelect("powerful_model", "Powerful model — for complex tasks",
						"", s.planoPowerfulModel, powerOpts),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.planoEndpoint = tuibridge.WizardString(f, "endpoint", s.planoEndpoint)
			s.planoFastModel = tuibridge.WizardString(f, "fast_model", "")
			s.planoPowerfulModel = tuibridge.WizardString(f, "powerful_model", "")
			return nil
		},
	})

	// Plano docker side-effect.
	steps = append(steps, tuibridge.WizardStep{
		Icon:            "⚡",
		Title:           "Smart Routing",
		SideEffectLabel: "Starting Plano via Docker",
		Skip:            func() bool { return !(s.secChoice == "configure" && s.usePlano) },
		SideEffect: func() error {
			if !setupPlanoDocker(s.planoEndpoint) {
				return fmt.Errorf("docker setup skipped — start manually: docker run -d -p 12000:12000 katanemo/plano:latest")
			}
			return nil
		},
	})

	// ── 5. Model routing ────────────────────────────────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:     "⚡",
		Title:    "Model Routing",
		Subtitle: "Assign specialized models for coding and vision tasks",
		Form: func() tuibridge.WizardFormSpec {
			if s.codingModel == "" {
				s.codingModel = "default"
			}
			if s.visionModel == "" {
				s.visionModel = "default"
			}
			codingOpts := []tuibridge.WizardOptionSpec{
				{Value: "default", Label: "Use Default"},
				{Value: "anthropic/claude-3-5-sonnet-20241022", Label: "Claude 3.5 Sonnet"},
				{Value: "openai/gpt-4o", Label: "GPT-4o"},
				{Value: "ollama/deepseek-r1", Label: "DeepSeek-R1 (Local)"},
			}
			visionOpts := []tuibridge.WizardOptionSpec{
				{Value: "default", Label: "Use Default"},
				{Value: "anthropic/claude-3-5-sonnet-20241022", Label: "Claude 3.5 Sonnet"},
				{Value: "openai/gpt-4o", Label: "GPT-4o"},
			}
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardSelect("coding", "Advanced Routing: Coding",
						"", s.codingModel, codingOpts),
					tuibridge.WizardSelect("vision", "Advanced Routing: Vision",
						"", s.visionModel, visionOpts),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.codingModel = tuibridge.WizardString(f, "coding", "default")
			s.visionModel = tuibridge.WizardString(f, "vision", "default")
			return nil
		},
	})

	// ── 6. Embeddings ───────────────────────────────────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:     "🔍",
		Title:    "Embeddings",
		Subtitle: "Semantic memory and search require an embedding model",
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardSelect("provider", "Embedding Provider",
						"Used for Semantic Memory (local learning).",
						s.embed.provider,
						[]tuibridge.WizardOptionSpec{
							{Value: "openai", Label: "OpenAI (Recommended)"},
							{Value: "azure", Label: "Azure OpenAI"},
							{Value: "ollama", Label: "Ollama (Local)"},
							{Value: "bedrock", Label: "AWS Bedrock"},
							{Value: "cohere", Label: "Cohere"},
							{Value: "google", Label: "Google Generative AI"},
							{Value: "voyage", Label: "Voyage AI"},
							{Value: "mistral", Label: "Mistral Native"},
							{Value: "vertex", Label: "Google Vertex AI (Gemini)"},
						}),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.embed.provider = tuibridge.WizardString(f, "provider", "")
			return nil
		},
	})

	// Embedding credentials (conditional on provider, dynamic fields).
	steps = append(steps, tuibridge.WizardStep{
		Icon:  "🔍",
		Title: "Embeddings — credentials",
		Skip: func() bool {
			return !(s.embed.provider != "" && s.embed.provider != "bedrock")
		},
		Form: func() tuibridge.WizardFormSpec {
			var fields []tuibridge.WizardFieldSpec
			switch s.embed.provider {
			case "ollama":
				if strings.TrimSpace(s.embed.baseURL) == "" {
					s.embed.baseURL = "http://localhost:11434"
				}
				fields = append(fields, tuibridge.WizardInput("base_url", "Ollama Base URL", "", s.embed.baseURL))
			case "azure":
				fields = append(fields,
					tuibridge.WizardInput("base_url", "Azure Endpoint", "", s.embed.baseURL),
					tuibridge.WizardPassword("api_key", "Azure API Key", ""),
				)
			default:
				fields = append(fields, tuibridge.WizardPassword("api_key",
					fmt.Sprintf("%s API Key (Embeddings)", s.embed.provider),
					"Stored safely in your local configuration."))
			}
			embedModels := map[string][]tuibridge.WizardOptionSpec{
				"openai":  {{Value: "text-embedding-3-small", Label: "text-embedding-3-small"}, {Value: "text-embedding-3-large", Label: "text-embedding-3-large"}},
				"azure":   {{Value: "text-embedding-3-small", Label: "text-embedding-3-small"}, {Value: "text-embedding-3-large", Label: "text-embedding-3-large"}},
				"ollama":  {{Value: "nomic-embed-text", Label: "nomic-embed-text"}, {Value: "mxbai-embed-large", Label: "mxbai-embed-large"}},
				"cohere":  {{Value: "embed-v4.0", Label: "embed-v4.0"}},
				"google":  {{Value: "text-embedding-004", Label: "text-embedding-004"}},
				"voyage":  {{Value: "voyage-3", Label: "voyage-3"}, {Value: "voyage-3-lite", Label: "voyage-3-lite"}},
				"mistral": {{Value: "mistral-embed", Label: "mistral-embed"}},
				"vertex":  {{Value: "text-embedding-004", Label: "text-embedding-004"}, {Value: "text-multilingual-embedding-002", Label: "text-multilingual-embedding-002"}},
			}
			if opts, ok := embedModels[s.embed.provider]; ok {
				fields = append(fields, tuibridge.WizardSelect("model", "Embedding Model",
					"", s.embed.model, opts))
			} else {
				fields = append(fields, tuibridge.WizardInput("model", "Embedding Model ID", "", s.embed.model))
			}
			return tuibridge.WizardFormSpec{Fields: fields}
		},
		OnSubmit: func(f map[string]any) error {
			if v := strings.TrimSpace(tuibridge.WizardString(f, "base_url", "")); v != "" {
				s.embed.baseURL = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "api_key", "")); v != "" {
				s.embed.apiKey = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "model", "")); v != "" {
				s.embed.model = v
			}
			return nil
		},
	})

	// ── 7. Local Gemma (side-effect only) ───────────────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:            "🪶",
		Title:           "Local Intelligence",
		SideEffectLabel: "Preparing Local Gemma model",
		Skip:            func() bool { return strings.TrimSpace(s.localIntelGGUF) != "" },
		SideEffect: func() error {
			if p := strings.TrimSpace(os.Getenv("OPSINTELLIGENCE_LOCAL_GEMMA_GGUF")); p != "" {
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					s.localIntelGGUF = p
					s.localIntelEnabled = true
					return nil
				}
			}
			stateDir := filepath.Dir(s.configPath)
			dst := localintel.DefaultGGUFPath(stateDir)
			if src, ok := discoverBundledGGUF(dst); ok {
				if err := copyFileAtomic(src, dst); err != nil {
					return fmt.Errorf("copy bundled Gemma: %w", err)
				}
				s.localIntelGGUF = dst
				s.localIntelEnabled = true
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
			defer cancel()
			res, err := localintel.BootstrapGGUF(ctx, localintel.BootstrapOptions{
				StateDir: stateDir,
				GGUFPath: dst,
			})
			if err != nil {
				return fmt.Errorf("Gemma download skipped: %w — run later: opsintelligence local-intel setup", err)
			}
			s.localIntelGGUF = res.Path
			s.localIntelEnabled = true
			return nil
		},
	})

	// ── 8. Memory (MemPalace) ───────────────────────────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:     "🧩",
		Title:    "Memory",
		Subtitle: "Structured hierarchical memory (requires Python 3.9+)",
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardConfirm("setup_mp",
						"Set up MemPalace now?",
						"Creates a Python venv and installs the mempalace PyPI package.\nSkip safely — run `opsintelligence quickstart` later.",
						s.setupMP, "Yes", "No"),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.setupMP = tuibridge.WizardBool(f, "setup_mp", false)
			return nil
		},
	})
	steps = append(steps, tuibridge.WizardStep{
		Icon:            "🧩",
		Title:           "Memory",
		SideEffectLabel: "Installing MemPalace",
		Skip:            func() bool { return !s.setupMP },
		SideEffect: func() error {
			stateDir := filepath.Dir(s.configPath)
			if err := runMemPalaceSetup(context.Background(), SetupOptions{StateDir: stateDir}); err != nil {
				return fmt.Errorf("MemPalace setup failed: %w — retry: opsintelligence quickstart", err)
			}
			s.memPalaceEnabled = true
			return nil
		},
	})

	// ── 9. Gateway & Access ─────────────────────────────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:     "🌐",
		Title:    "Gateway & Access",
		Subtitle: "Configure how the agent API is exposed on your network",
		Form: func() tuibridge.WizardFormSpec {
			gwMode := s.gwMode
			if gwMode == "" {
				gwMode = "loopback"
			}
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardSelect("gw_mode", "Remote Access Mode", "", gwMode,
						[]tuibridge.WizardOptionSpec{
							{Value: "loopback", Label: "Local Only (127.0.0.1)"},
							{Value: "lan", Label: "Local Network (LAN — 0.0.0.0)"},
							{Value: "tailscale", Label: "Tailscale (Secure VPN)"},
						}),
					tuibridge.WizardInput("gw_host", "Gateway Host", "", s.gwHost),
					tuibridge.WizardInput("gw_port", "Gateway Port",
						"Must be a number between 1 and 65535.",
						strconv.Itoa(s.gwPort)),
					tuibridge.WizardPassword("gw_token", "Security Token (leave blank to auto-generate)",
						"Password to protect your Gateway API. Leave empty for auto-generate."),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.gwMode = tuibridge.WizardString(f, "gw_mode", s.gwMode)
			if v := strings.TrimSpace(tuibridge.WizardString(f, "gw_host", "")); v != "" {
				s.gwHost = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "gw_port", "")); v != "" {
				if p, err := strconv.Atoi(v); err == nil && p >= 1 && p <= 65535 {
					s.gwPort = p
				}
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "gw_token", "")); v != "" {
				s.gwToken = v
			}
			return nil
		},
	})

	// Auto-generate token if user left it blank.
	steps = append(steps, tuibridge.WizardStep{
		Icon:            "🌐",
		Title:           "Gateway & Access",
		SideEffectLabel: "Generating Gateway token",
		Skip:            func() bool { return strings.TrimSpace(s.gwToken) != "" },
		SideEffect: func() error {
			s.gwToken = randomToken(24)
			return nil
		},
	})

	// Tailscale config (only when bind = tailscale).
	steps = append(steps, tuibridge.WizardStep{
		Icon:  "🌐",
		Title: "Gateway — Tailscale",
		Skip:  func() bool { return s.gwMode != "tailscale" },
		Form: func() tuibridge.WizardFormSpec {
			detected := detectTailscaleHostname()
			if detected != "" && placeholderGatewayHost(s.gwHost) {
				s.gwHost = detected
			}
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardSelect("ts_mode", "Tailscale Mode", "", s.tsMode,
						[]tuibridge.WizardOptionSpec{
							{Value: "off", Label: "Off"},
							{Value: "serve", Label: "Serve (Local Tailnet)"},
							{Value: "funnel", Label: "Funnel (Public Internet via Tailscale)"},
						}),
					tuibridge.WizardInput("ts_host", "Tailscale Hostname",
						"Your machine's Tailscale FQDN (e.g. opsintelligence.tail1234.ts.net).\nAuto-detected when possible — required for Funnel public URLs.",
						s.gwHost),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.tsMode = tuibridge.WizardString(f, "ts_mode", s.tsMode)
			if v := strings.TrimSpace(tuibridge.WizardString(f, "ts_host", "")); v != "" {
				if strings.EqualFold(strings.TrimSpace(s.tsMode), "funnel") && placeholderGatewayHost(v) {
					return fmt.Errorf("need a real *.ts.net hostname for Funnel; got %q", v)
				}
				s.gwHost = v
			}
			return nil
		},
	})

	// Host Tailscale Funnel option (only when bind = loopback or lan).
	steps = append(steps, tuibridge.WizardStep{
		Icon:  "🌐",
		Title: "Gateway — Expose via Tailscale Funnel",
		Skip:  func() bool { return !(s.gwMode == "loopback" || s.gwMode == "lan") },
		Form: func() tuibridge.WizardFormSpec {
			if !strings.EqualFold(strings.TrimSpace(s.tsMode), "funnel") {
				s.tsMode = "off"
			}
			if detected := detectTailscaleHostname(); detected != "" && placeholderGatewayHost(s.gwHost) {
				s.gwHost = detected
			}
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardSelect("ts_mode", "Expose via Tailscale Funnel?",
						"Runs `tailscale funnel <port>` on startup so Microsoft's Bot\nFramework servers can reach your gateway for Teams webhooks.",
						s.tsMode,
						[]tuibridge.WizardOptionSpec{
							{Value: "off", Label: "No — keep local only"},
							{Value: "funnel", Label: "Yes — use host Tailscale Funnel"},
						}),
					tuibridge.WizardInput("ts_host", "Machine Tailscale Hostname",
						"Your Tailscale FQDN (e.g. myhost.tail1234.ts.net).\nAuto-detected when possible — needed for the Teams webhook URL.",
						s.gwHost),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.tsMode = tuibridge.WizardString(f, "ts_mode", s.tsMode)
			if v := strings.TrimSpace(tuibridge.WizardString(f, "ts_host", "")); v != "" {
				if strings.EqualFold(strings.TrimSpace(s.tsMode), "funnel") && placeholderGatewayHost(v) {
					return fmt.Errorf("need your machine's *.ts.net hostname (run: tailscale status)")
				}
				s.gwHost = v
			}
			return nil
		},
	})

	// ── 10. Messaging Channels — multi-select gate ──────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:     "💬",
		Title:    "Messaging Channels",
		Subtitle: "Connect Telegram, Slack, Discord, WhatsApp, or Teams",
		Form: func() tuibridge.WizardFormSpec {
			tgLabel, dcLabel, slLabel, waLabel, msLabel :=
				"Telegram", "Discord", "Slack", "WhatsApp", "Microsoft Teams"
			if s.existing != nil {
				if s.existing.Channels.Telegram != nil && s.existing.Channels.Telegram.BotToken != "" {
					tgLabel = "Telegram ✓"
				}
				if s.existing.Channels.Discord != nil && s.existing.Channels.Discord.BotToken != "" {
					dcLabel = "Discord ✓"
				}
				if s.existing.Channels.Slack != nil && s.existing.Channels.Slack.BotToken != "" {
					slLabel = "Slack ✓"
				}
				if s.existing.Channels.WhatsApp != nil {
					waLabel = "WhatsApp ✓"
				}
				if s.existing.Channels.Teams != nil && s.existing.Channels.Teams.AppID != "" {
					msLabel = "Microsoft Teams ✓"
				}
			}
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardMultiSelect("channels",
						"Select messaging channels to enable",
						"Configure credentials for each selected channel next.",
						s.selectedChannels,
						[]tuibridge.WizardOptionSpec{
							{Value: "telegram", Label: tgLabel},
							{Value: "discord", Label: dcLabel},
							{Value: "slack", Label: slLabel},
							{Value: "whatsapp", Label: waLabel},
							{Value: "msteams", Label: msLabel},
						}),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.selectedChannels = tuibridge.WizardStrings(f, "channels", nil)
			return nil
		},
	})

	// Telegram credentials.
	steps = append(steps, tuibridge.WizardStep{
		Icon: "💬", Title: "Telegram Setup",
		Skip: func() bool { return !containsStr(s.selectedChannels, "telegram") },
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardPassword("bot_token", "Telegram Bot Token", ""),
					tuibridge.WizardSelect("dm_mode", "Telegram Security Mode", "",
						s.tgDMMode, channelModeOptionsWizard()),
					tuibridge.WizardInput("allow_from", "Whitelisted Telegram IDs",
						"Comma-separated IDs/usernames. Only for Allowlist mode.",
						s.tgAllowFromRaw),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			if v := strings.TrimSpace(tuibridge.WizardString(f, "bot_token", "")); v != "" {
				s.tgBotToken = v
			}
			s.tgDMMode = tuibridge.WizardString(f, "dm_mode", s.tgDMMode)
			s.tgAllowFromRaw = tuibridge.WizardString(f, "allow_from", s.tgAllowFromRaw)
			return nil
		},
	})

	// Discord credentials.
	steps = append(steps, tuibridge.WizardStep{
		Icon: "💬", Title: "Discord Setup",
		Skip: func() bool { return !containsStr(s.selectedChannels, "discord") },
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardPassword("bot_token", "Discord Bot Token", ""),
					tuibridge.WizardSelect("dm_mode", "Discord Security Mode", "",
						s.dcDMMode, channelModeOptionsWizard()),
					tuibridge.WizardInput("allow_from", "Whitelisted Discord IDs",
						"Comma-separated numeric IDs. Only for Allowlist mode.",
						s.dcAllowFromRaw),
					tuibridge.WizardConfirm("require_mention",
						"Require @bot mention in server channels?",
						"Recommended: avoids noise in busy guild channels. DMs are unaffected.",
						s.dcRequireMention, "Yes", "No"),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			if v := strings.TrimSpace(tuibridge.WizardString(f, "bot_token", "")); v != "" {
				s.dcBotToken = v
			}
			s.dcDMMode = tuibridge.WizardString(f, "dm_mode", s.dcDMMode)
			s.dcAllowFromRaw = tuibridge.WizardString(f, "allow_from", s.dcAllowFromRaw)
			s.dcRequireMention = tuibridge.WizardBool(f, "require_mention", s.dcRequireMention)
			return nil
		},
	})

	// Slack credentials.
	steps = append(steps, tuibridge.WizardStep{
		Icon: "💬", Title: "Slack Setup",
		Skip: func() bool { return !containsStr(s.selectedChannels, "slack") },
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardPassword("bot_token", "Slack Bot Token", ""),
					tuibridge.WizardPassword("app_token", "Slack App Token", ""),
					tuibridge.WizardSelect("dm_mode", "Slack Security Mode", "",
						s.slDMMode, channelModeOptionsWizard()),
					tuibridge.WizardInput("allow_from", "Whitelisted Slack IDs",
						"Comma-separated numeric IDs. Only for Allowlist mode.",
						s.slAllowFromRaw),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			if v := strings.TrimSpace(tuibridge.WizardString(f, "bot_token", "")); v != "" {
				s.slBotToken = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "app_token", "")); v != "" {
				s.slAppToken = v
			}
			s.slDMMode = tuibridge.WizardString(f, "dm_mode", s.slDMMode)
			s.slAllowFromRaw = tuibridge.WizardString(f, "allow_from", s.slAllowFromRaw)
			return nil
		},
	})

	// WhatsApp credentials.
	steps = append(steps, tuibridge.WizardStep{
		Icon: "💬", Title: "WhatsApp Setup",
		Skip: func() bool { return !containsStr(s.selectedChannels, "whatsapp") },
		Form: func() tuibridge.WizardFormSpec {
			if strings.TrimSpace(s.waSessionID) == "" {
				s.waSessionID = "personal"
			}
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardInput("session_id", "WhatsApp Session ID",
						"A name for this session (e.g. 'personal'). QR scan required after setup.",
						s.waSessionID),
					tuibridge.WizardSelect("dm_mode", "WhatsApp Security Mode", "",
						s.waDMMode, channelModeOptionsWizard()),
					tuibridge.WizardInput("allow_from", "Whitelisted Numbers",
						"Comma-separated (e.g. '1234567890, 9876543210'). Only for Allowlist mode.",
						s.waAllowFromRaw),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.waSessionID = tuibridge.WizardString(f, "session_id", s.waSessionID)
			s.waDMMode = tuibridge.WizardString(f, "dm_mode", s.waDMMode)
			s.waAllowFromRaw = tuibridge.WizardString(f, "allow_from", s.waAllowFromRaw)
			return nil
		},
	})

	// Microsoft Teams credentials.
	steps = append(steps, tuibridge.WizardStep{
		Icon: "💬", Title: "Microsoft Teams Setup",
		Skip: func() bool { return !containsStr(s.selectedChannels, "msteams") },
		Form: func() tuibridge.WizardFormSpec {
			funnelMode := s.gwMode == "tailscale" && strings.EqualFold(strings.TrimSpace(s.tsMode), "funnel")
			appIDDesc := "Microsoft App ID from your Azure Bot registration."
			if funnelMode {
				appIDDesc = "Microsoft App ID from your Azure Bot registration.\nWebhook will be served via Tailscale Funnel at <your-funnel-url>/teams/api/messages"
			}
			fields := []tuibridge.WizardFieldSpec{
				tuibridge.WizardInput("app_id", "Azure Bot App ID", appIDDesc, s.teamsAppID),
				tuibridge.WizardPassword("app_password", "Azure Bot App Password", ""),
			}
			if !funnelMode {
				fields = append(fields, tuibridge.WizardInput("listen_addr",
					"Webhook Listen Address",
					"Port for Bot Framework webhook (default :3978).",
					s.teamsListenAddr))
			}
			fields = append(fields,
				tuibridge.WizardSelect("dm_mode", "Teams Security Mode", "",
					s.teamsDMMode,
					[]tuibridge.WizardOptionSpec{
						{Value: "allowlist", Label: "Allowlist (Recommended)"},
						{Value: "open", Label: "Open (any authenticated Teams user)"},
						{Value: "disabled", Label: "Disabled"},
					}),
				tuibridge.WizardInput("allow_from", "Allowed Teams User IDs",
					"Comma-separated AAD object IDs. Leave empty for open mode.",
					s.teamsAllowFromRaw),
			)
			return tuibridge.WizardFormSpec{Fields: fields}
		},
		OnSubmit: func(f map[string]any) error {
			s.teamsAppID = tuibridge.WizardString(f, "app_id", s.teamsAppID)
			if v := strings.TrimSpace(tuibridge.WizardString(f, "app_password", "")); v != "" {
				s.teamsAppPassword = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "listen_addr", "")); v != "" {
				s.teamsListenAddr = v
			}
			s.teamsDMMode = tuibridge.WizardString(f, "dm_mode", s.teamsDMMode)
			s.teamsAllowFromRaw = tuibridge.WizardString(f, "allow_from", s.teamsAllowFromRaw)
			return nil
		},
	})

	// ── 11. Skills ──────────────────────────────────────────────────────────
	const customSkillSentinel = "__custom__"
	var customSkillPath string

	steps = append(steps, tuibridge.WizardStep{
		Icon:     "🛠",
		Title:    "Skills",
		Subtitle: "Install built-in and custom agent skills",
		Form: func() tuibridge.WizardFormSpec {
			home, _ := os.UserHomeDir()
			bundledDir := filepath.Join(home, ".opsintelligence", "skills", "bundled")
			customDir := filepath.Join(home, ".opsintelligence", "skills", "custom")
			_ = os.MkdirAll(bundledDir, 0o755)
			_ = os.MkdirAll(customDir, 0o755)
			if src := resolveBundledSkillsSrc(); src != "" {
				_ = skills.CopyDir(src, bundledDir)
			}
			mp := skills.NewMarketplace(bundledDir, customDir)
			idx, err := mp.FetchIndex(context.Background())
			if err != nil {
				return tuibridge.WizardFormSpec{
					Fields: []tuibridge.WizardFieldSpec{
						tuibridge.WizardNote("Skills",
							"Could not reach the skills index.\nYou can enable skills later by editing your config."),
					},
				}
			}
			var opts []tuibridge.WizardOptionSpec
			inIndex := make(map[string]bool)
			for _, e := range idx.Skills {
				inIndex[e.Name] = true
				label := e.Name
				if e.Emoji != "" {
					label = e.Emoji + "  " + e.Name
				}
				if e.Description != "" {
					desc := e.Description
					if len(desc) > 72 {
						desc = desc[:71] + "…"
					}
					label += " — " + desc
				}
				opts = append(opts, tuibridge.WizardOptionSpec{Value: e.Name, Label: label})
			}
			for _, name := range s.selectedSkills {
				if name != "" && !inIndex[name] {
					opts = append(opts, tuibridge.WizardOptionSpec{Value: name, Label: name + " (from config)"})
				}
			}
			opts = append(opts, tuibridge.WizardOptionSpec{
				Value: customSkillSentinel,
				Label: "＋  Add custom skill  (local path or URL)",
			})
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardMultiSelect("skills", "Skills to enable",
						"Space toggles · Enter confirms. Bundled skills fetch automatically when missing.",
						s.selectedSkills, opts),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.selectedSkills = tuibridge.WizardStrings(f, "skills", nil)
			return nil
		},
	})
	steps = append(steps, tuibridge.WizardStep{
		Icon: "🛠", Title: "Custom Skill",
		Skip: func() bool { return !containsStr(s.selectedSkills, customSkillSentinel) },
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardInput("path", "Custom skill path or URL",
						"/path/to/skill  or  https://github.com/user/skill",
						""),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			customSkillPath = strings.TrimSpace(tuibridge.WizardString(f, "path", ""))
			return nil
		},
	})
	steps = append(steps, tuibridge.WizardStep{
		Icon: "🛠", Title: "Skills",
		SideEffectLabel: "Installing selected skills",
		SideEffect: func() error {
			home, _ := os.UserHomeDir()
			customDir := filepath.Join(home, ".opsintelligence", "skills", "custom")
			bundledDir := filepath.Join(home, ".opsintelligence", "skills", "bundled")
			mp := skills.NewMarketplace(bundledDir, customDir)
			var kept []string
			for _, n := range s.selectedSkills {
				if n == "" || n == customSkillSentinel {
					if n == customSkillSentinel && strings.TrimSpace(customSkillPath) != "" {
						dest, err := mp.InstallFromPath(customSkillPath)
						if err != nil {
							dest, err = mp.Install(context.Background(), customSkillPath)
						}
						if err == nil {
							kept = append(kept, filepath.Base(dest))
						}
					}
					continue
				}
				dest := filepath.Join(customDir, n)
				if _, err := os.Stat(dest); os.IsNotExist(err) {
					if _, err := mp.Install(context.Background(), n); err != nil {
						continue
					}
				}
				kept = append(kept, n)
			}
			s.selectedSkills = kept
			return nil
		},
	})

	// ── 12. DevOps Integrations ─────────────────────────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:     "⚙",
		Title:    "DevOps Integrations",
		Subtitle: "GitHub, GitLab, Jenkins, SonarQube (all optional)",
		Form: func() tuibridge.WizardFormSpec {
			if s.existing != nil {
				if s.githubToken != "" || strings.TrimSpace(s.githubTokenEnv) != "" ||
					s.gitlabToken != "" || strings.TrimSpace(s.gitlabTokenEnv) != "" ||
					(strings.TrimSpace(s.jenkinsURL) != "" && (s.jenkinsToken != "" || strings.TrimSpace(s.jenkinsTokenEnv) != "")) ||
					(strings.TrimSpace(s.sonarURL) != "" && (s.sonarToken != "" || strings.TrimSpace(s.sonarTokenEnv) != "")) {
					s.configureDevOps = true
				}
			}
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardConfirm("configure_devops",
						"Configure DevOps API integrations now?",
						"GitHub PAT, GitLab, Jenkins, Sonar — used for PR review and REST APIs.\nSkip to leave existing devops: block unchanged.",
						s.configureDevOps, "Yes", "No"),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.configureDevOps = tuibridge.WizardBool(f, "configure_devops", s.configureDevOps)
			return nil
		},
	})
	steps = append(steps, tuibridge.WizardStep{
		Icon: "⚙", Title: "DevOps — Credentials",
		Skip: func() bool { return !s.configureDevOps },
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardInput("github_base", "GitHub API base URL (optional)",
						"Leave blank for https://api.github.com.", s.githubBaseURL),
					tuibridge.WizardInput("github_org", "GitHub default org (optional)", "", s.githubDefaultOrg),
					tuibridge.WizardPassword("github_token", "GitHub personal access token (optional)", ""),
					tuibridge.WizardInput("github_token_env", "Or: GitHub token env var (optional)",
						"e.g. GITHUB_TOKEN", s.githubTokenEnv),
					tuibridge.WizardInput("gitlab_url", "GitLab base URL (optional)", "", s.gitlabURL),
					tuibridge.WizardPassword("gitlab_token", "GitLab token (optional)", ""),
					tuibridge.WizardInput("gitlab_token_env", "Or: GitLab token env var (optional)", "", s.gitlabTokenEnv),
					tuibridge.WizardInput("jenkins_url", "Jenkins base URL (optional)", "", s.jenkinsURL),
					tuibridge.WizardInput("jenkins_user", "Jenkins user (optional)", "", s.jenkinsUser),
					tuibridge.WizardPassword("jenkins_token", "Jenkins token (optional)", ""),
					tuibridge.WizardInput("jenkins_token_env", "Or: Jenkins token env var (optional)", "", s.jenkinsTokenEnv),
					tuibridge.WizardInput("sonar_url", "SonarQube base URL (optional)", "", s.sonarURL),
					tuibridge.WizardPassword("sonar_token", "SonarQube token (optional)", ""),
					tuibridge.WizardInput("sonar_token_env", "Or: Sonar token env var (optional)", "", s.sonarTokenEnv),
					tuibridge.WizardInput("sonar_prefix", "Sonar project key prefix (optional)", "", s.sonarProjectPrefix),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			assign := func(key string, dst *string) {
				if v := strings.TrimSpace(tuibridge.WizardString(f, key, "")); v != "" {
					*dst = v
				}
			}
			assign("github_base", &s.githubBaseURL)
			assign("github_org", &s.githubDefaultOrg)
			assign("github_token", &s.githubToken)
			assign("github_token_env", &s.githubTokenEnv)
			assign("gitlab_url", &s.gitlabURL)
			assign("gitlab_token", &s.gitlabToken)
			assign("gitlab_token_env", &s.gitlabTokenEnv)
			assign("jenkins_url", &s.jenkinsURL)
			assign("jenkins_user", &s.jenkinsUser)
			assign("jenkins_token", &s.jenkinsToken)
			assign("jenkins_token_env", &s.jenkinsTokenEnv)
			assign("sonar_url", &s.sonarURL)
			assign("sonar_token", &s.sonarToken)
			assign("sonar_token_env", &s.sonarTokenEnv)
			assign("sonar_prefix", &s.sonarProjectPrefix)
			return nil
		},
	})

	// GitHub webhook (independent of the DevOps configure gate).
	steps = append(steps, tuibridge.WizardStep{
		Icon:     "⚙",
		Title:    "GitHub Webhook",
		Subtitle: "Enable GitHub → gateway webhook adapter",
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardConfirm("gh_webhook",
						"Enable GitHub → gateway webhook adapter?",
						"When enabled, set the same signing secret in GitHub → Settings → Webhooks.\nSkip to leave existing webhooks: block unchanged.",
						s.ghWebhookEnabled, "Yes", "No"),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			s.ghWebhookEnabled = tuibridge.WizardBool(f, "gh_webhook", s.ghWebhookEnabled)
			return nil
		},
	})
	steps = append(steps, tuibridge.WizardStep{
		Icon: "⚙", Title: "GitHub Webhook — Secret",
		Skip: func() bool { return !s.ghWebhookEnabled },
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardPassword("secret", "Webhook signing secret",
						"Repository/org webhook secret (X-Hub-Signature-256). Not your PAT."),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			if v := strings.TrimSpace(tuibridge.WizardString(f, "secret", "")); v != "" {
				s.ghWebhookSecret = v
			}
			return nil
		},
	})

	// ── 13. Save configuration ──────────────────────────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:            "💾",
		Title:           "Save",
		SideEffectLabel: "Merging and saving configuration",
		SideEffect: func() error {
			yaml := buildConfigYAML(s)
			merged, err := mergeOnboardYAML(s.configPath, []byte(yaml))
			if err != nil {
				return fmt.Errorf("merge: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(s.configPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(s.configPath, merged, 0o600); err != nil {
				return err
			}
			s.done = true
			return nil
		},
	})

	// ── 14. Login service install (macOS/Linux only) ────────────────────────
	steps = append(steps, tuibridge.WizardStep{
		Icon:            "🚀",
		Title:           "Auto-start",
		SideEffectLabel: "Registering login service (auto-start)",
		Skip:            func() bool { return runtime.GOOS != "darwin" && runtime.GOOS != "linux" },
		SideEffect: func() error {
			if root := filepath.Dir(s.configPath); root != "" && root != "." {
				_ = os.Setenv("OPSINTELLIGENCE_STATE_DIR", root)
			}
			return installService()
		},
	})

	return steps, s
}

// providerStepsWizard returns the 5 sub-steps for one provider entry: provider
// select → bedrock auth mode → credentials → model select → custom model.
// This is the wizard-engine port of providerSteps() in onboard_steps.go.
//
//   icon, title, subtitle — header chrome shown above every sub-step
//   entry                  — pointer-bound state struct (s.primary / s.secondary)
//   isPrimary              — switches the option list between full primary set
//                            and the recommended-for-secondary subset
//   bedrockAuthMode        — pointer-bound auth mode for Bedrock; matches the
//                            behaviour of the legacy code so each provider
//                            entry gets its own mode value
//   extraCond              — outer gate (e.g. "only show if secondary == configure")
func providerStepsWizard(
	icon, title, subtitle string,
	entry *provEntry,
	isPrimary bool,
	bedrockAuthMode *string,
	extraCond func() bool,
) []tuibridge.WizardStep {
	skip := func(extra func() bool) func() bool {
		return func() bool {
			if extraCond != nil && !extraCond() {
				return true
			}
			if extra != nil && extra() {
				return true
			}
			return false
		}
	}

	allPrimary := []tuibridge.WizardOptionSpec{
		{Value: "anthropic", Label: "Anthropic (Recommended)"},
		{Value: "openai", Label: "OpenAI"},
		{Value: "ollama", Label: "Ollama (Local / Free)"},
		{Value: "bedrock", Label: "AWS Bedrock"},
		{Value: "gemini", Label: "Google Gemini (AI Studio)"},
		{Value: "vertex", Label: "Google Vertex AI (Gemini)"},
		{Value: "groq", Label: "Groq"},
		{Value: "mistral", Label: "Mistral"},
		{Value: "deepseek", Label: "DeepSeek"},
		{Value: "xai", Label: "xAI (Grok)"},
		{Value: "perplexity", Label: "Perplexity"},
		{Value: "openrouter", Label: "OpenRouter"},
		{Value: "nvidia", Label: "NVIDIA NIM"},
		{Value: "together", Label: "Together AI"},
		{Value: "huggingface", Label: "HuggingFace (Inference API)"},
		{Value: "cohere", Label: "Cohere"},
		{Value: "azure", Label: "Azure OpenAI"},
		{Value: "vllm", Label: "vLLM (Local / Custom)"},
		{Value: "lmstudio", Label: "LM Studio (Local)"},
	}
	allSecondary := []tuibridge.WizardOptionSpec{
		{Value: "ollama", Label: "Ollama (Local / Free)"},
		{Value: "openai", Label: "OpenAI"},
		{Value: "groq", Label: "Groq (Super Fast)"},
		{Value: "anthropic", Label: "Anthropic"},
		{Value: "mistral", Label: "Mistral"},
		{Value: "openrouter", Label: "OpenRouter"},
		{Value: "azure", Label: "Azure OpenAI"},
		{Value: "deepseek", Label: "DeepSeek"},
		{Value: "xai", Label: "xAI (Grok)"},
		{Value: "perplexity", Label: "Perplexity"},
		{Value: "bedrock", Label: "AWS Bedrock"},
		{Value: "gemini", Label: "Google Gemini (AI Studio)"},
		{Value: "vertex", Label: "Google Vertex AI (Gemini)"},
		{Value: "nvidia", Label: "NVIDIA NIM"},
		{Value: "together", Label: "Together AI"},
		{Value: "huggingface", Label: "HuggingFace (Inference API)"},
		{Value: "cohere", Label: "Cohere"},
		{Value: "vllm", Label: "vLLM (Local / Custom)"},
		{Value: "lmstudio", Label: "LM Studio (Local)"},
	}
	provOpts := allSecondary
	provTitle := "Which secondary AI provider would you like to use?"
	if isPrimary {
		provOpts = allPrimary
		provTitle = "Which primary AI provider would you like to use?"
	}

	needsKey := map[string]bool{
		"anthropic": true, "openai": true, "gemini": true, "groq": true, "mistral": true,
		"openrouter": true, "azure": true, "deepseek": true, "perplexity": true,
		"xai": true, "together": true, "nvidia": true, "cohere": true,
		"huggingface": true, "voyage": true,
	}
	needsURL := map[string]bool{
		"ollama": true, "vllm": true, "lmstudio": true,
	}
	needsCreds := func() bool {
		return needsKey[entry.provider] || needsURL[entry.provider] ||
			entry.provider == "bedrock" || entry.provider == "vertex" || entry.provider == "azure"
	}
	defaultURL := map[string]string{
		"ollama":   "http://localhost:11434",
		"vllm":     "http://localhost:8000/v1",
		"lmstudio": "http://localhost:1234/v1",
		"azure":    "https://YOUR_RESOURCE_NAME.openai.azure.com",
	}

	steps := make([]tuibridge.WizardStep, 0, 5)

	// Step 1: provider select.
	steps = append(steps, tuibridge.WizardStep{
		Icon: icon, Title: title, Subtitle: subtitle,
		Skip: skip(nil),
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardSelect("provider", provTitle, "", entry.provider, provOpts),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			entry.provider = tuibridge.WizardString(f, "provider", entry.provider)
			return nil
		},
	})

	// Step 2: Bedrock auth mode.
	steps = append(steps, tuibridge.WizardStep{
		Icon: icon, Title: title,
		Skip: skip(func() bool { return entry.provider != "bedrock" }),
		Form: func() tuibridge.WizardFormSpec {
			if *bedrockAuthMode == "" {
				*bedrockAuthMode = inferBedrockAuthMode(*entry)
			}
			if strings.TrimSpace(entry.awsRegion) == "" {
				entry.awsRegion = "us-east-1"
			}
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardSelect("auth", "AWS Bedrock Authentication",
						"Tip: Bearer Tokens are most stable in us-east-1.",
						*bedrockAuthMode,
						[]tuibridge.WizardOptionSpec{
							{Value: "iam", Label: "Direct IAM Keys (AccessKeyID/SecretKey)"},
							{Value: "profile", Label: "AWS Named Profile (~/.aws/credentials)"},
							{Value: "api_key", Label: "Native Bedrock API Key (Bearer Token)"},
						}),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			*bedrockAuthMode = tuibridge.WizardString(f, "auth", *bedrockAuthMode)
			return nil
		},
	})

	// Step 3: credentials (dynamic fields per provider).
	steps = append(steps, tuibridge.WizardStep{
		Icon: icon, Title: title,
		Skip: skip(func() bool { return !needsCreds() }),
		Form: func() tuibridge.WizardFormSpec {
			var fields []tuibridge.WizardFieldSpec
			if needsKey[entry.provider] {
				fields = append(fields, tuibridge.WizardPassword("api_key",
					"Enter API Key",
					"Stored safely in your local configuration."))
			}
			if def, ok := defaultURL[entry.provider]; ok {
				if strings.TrimSpace(entry.baseURL) == "" {
					entry.baseURL = def
				}
				fields = append(fields, tuibridge.WizardInput("base_url",
					fmt.Sprintf("Base URL (default: %s)", def),
					"", entry.baseURL))
			}
			if entry.provider == "azure" {
				fields = append(fields, tuibridge.WizardInput("api_version",
					"Azure API Version (e.g., 2024-02-15-preview)",
					"", entry.apiVersion))
			}
			if entry.provider == "bedrock" {
				fields = append(fields, tuibridge.WizardInput("aws_region", "AWS Region",
					"", entry.awsRegion))
				switch *bedrockAuthMode {
				case "iam":
					fields = append(fields,
						tuibridge.WizardInput("aws_access_key", "AWS Access Key ID",
							"", entry.awsAccessKey),
						tuibridge.WizardPassword("aws_secret_key", "AWS Secret Access Key", ""),
					)
				case "profile":
					if entry.awsProfile == "" {
						entry.awsProfile = "default"
					}
					fields = append(fields, tuibridge.WizardInput("aws_profile",
						"AWS Profile", "", entry.awsProfile))
				case "api_key":
					fields = append(fields, tuibridge.WizardPassword("api_key",
						"Bedrock API Key", ""))
				}
			}
			if entry.provider == "vertex" {
				fields = append(fields,
					tuibridge.WizardInput("vertex_project", "GCP Project ID",
						"", entry.vertexProj),
					tuibridge.WizardInput("vertex_location",
						"GCP Location (e.g. us-central1)", "", entry.vertexLoc),
					tuibridge.WizardInput("vertex_creds",
						"Service Account JSON Path (Optional)", "", entry.vertexCreds),
				)
			}
			if len(fields) == 0 {
				fields = append(fields, tuibridge.WizardNote(title,
					"No credentials needed for this provider."))
			}
			return tuibridge.WizardFormSpec{Fields: fields}
		},
		OnSubmit: func(f map[string]any) error {
			if v := strings.TrimSpace(tuibridge.WizardString(f, "api_key", "")); v != "" {
				entry.apiKey = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "base_url", "")); v != "" {
				entry.baseURL = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "api_version", "")); v != "" {
				entry.apiVersion = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "aws_region", "")); v != "" {
				entry.awsRegion = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "aws_access_key", "")); v != "" {
				entry.awsAccessKey = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "aws_secret_key", "")); v != "" {
				entry.awsSecretKey = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "aws_profile", "")); v != "" {
				entry.awsProfile = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "vertex_project", "")); v != "" {
				entry.vertexProj = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "vertex_location", "")); v != "" {
				entry.vertexLoc = v
			}
			if v := strings.TrimSpace(tuibridge.WizardString(f, "vertex_creds", "")); v != "" {
				entry.vertexCreds = v
			}
			return nil
		},
	})

	// Step 4: model select.
	modelChoices := map[string][]tuibridge.WizardOptionSpec{
		"anthropic": {
			{Value: "claude-3-7-sonnet-20250219", Label: "Claude 3.7 Sonnet (Latest)"},
			{Value: "claude-3-5-sonnet-20241022", Label: "Claude 3.5 Sonnet (Classic)"},
			{Value: "claude-3-5-haiku-20241022", Label: "Claude 3.5 Haiku (Fast)"},
			{Value: "custom", Label: "Other / Custom…"},
		},
		"openai": {
			{Value: "gpt-4o", Label: "GPT-4o (Smartest)"},
			{Value: "gpt-4o-mini", Label: "GPT-4o-mini (Efficient)"},
			{Value: "o3-mini", Label: "o3-mini (Reasoning)"},
			{Value: "custom", Label: "Other / Custom…"},
		},
		"ollama": {
			{Value: "llama3.2", Label: "Llama 3.2 (3B)"},
			{Value: "mistral", Label: "Mistral (7B)"},
			{Value: "deepseek-r1", Label: "DeepSeek-R1 (70B Distill)"},
			{Value: "custom", Label: "Other / Custom…"},
		},
		"groq": {
			{Value: "llama-3.3-70b-versatile", Label: "Llama 3.3 70B Versatile"},
			{Value: "mixtral-8x7b-32768", Label: "Mixtral 8x7B"},
			{Value: "custom", Label: "Other / Custom…"},
		},
		"deepseek": {
			{Value: "deepseek-chat", Label: "DeepSeek Chat"},
			{Value: "deepseek-reasoner", Label: "DeepSeek Reasoner (R1)"},
			{Value: "custom", Label: "Other / Custom…"},
		},
		"perplexity": {
			{Value: "sonar-reasoning-pro", Label: "Sonar Reasoning Pro"},
			{Value: "sonar-pro", Label: "Sonar Pro"},
			{Value: "custom", Label: "Other / Custom…"},
		},
		"xai": {
			{Value: "grok-4-latest", Label: "Grok 4"},
			{Value: "grok-beta", Label: "Grok Beta"},
			{Value: "custom", Label: "Other / Custom…"},
		},
		"gemini": {
			{Value: "gemini-2.5-flash", Label: "Gemini 2.5 Flash"},
			{Value: "gemini-2.5-pro", Label: "Gemini 2.5 Pro"},
			{Value: "gemini-2.0-flash", Label: "Gemini 2.0 Flash"},
			{Value: "gemini-1.5-flash", Label: "Gemini 1.5 Flash"},
			{Value: "gemini-1.5-pro", Label: "Gemini 1.5 Pro"},
			{Value: "custom", Label: "Other / Custom…"},
		},
		"vertex": {
			{Value: "gemini-1.5-pro", Label: "Gemini 1.5 Pro"},
			{Value: "gemini-1.5-flash", Label: "Gemini 1.5 Flash"},
			{Value: "gemini-2.0-flash-exp", Label: "Gemini 2.0 Flash Exp"},
			{Value: "custom", Label: "Other / Custom…"},
		},
	}
	steps = append(steps, tuibridge.WizardStep{
		Icon: icon, Title: title,
		Skip: skip(nil),
		Form: func() tuibridge.WizardFormSpec {
			if entry.provider == "bedrock" {
				return bedrockModelFormSpec(entry)
			}
			if opts, ok := modelChoices[entry.provider]; ok {
				return tuibridge.WizardFormSpec{
					Fields: []tuibridge.WizardFieldSpec{
						tuibridge.WizardSelect("model", "Select Model", "", entry.model, opts),
					},
				}
			}
			// Free-text for providers without a curated list.
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardInput("model", "Enter Model ID", "", entry.model),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			entry.model = tuibridge.WizardString(f, "model", entry.model)
			return nil
		},
	})

	// Step 5: custom model fallback.
	steps = append(steps, tuibridge.WizardStep{
		Icon: icon, Title: title,
		Skip: skip(func() bool { return entry.model != "custom" }),
		Form: func() tuibridge.WizardFormSpec {
			return tuibridge.WizardFormSpec{
				Fields: []tuibridge.WizardFieldSpec{
					tuibridge.WizardInput("model", "Enter Custom Model ID", "", ""),
				},
			}
		},
		OnSubmit: func(f map[string]any) error {
			if v := strings.TrimSpace(tuibridge.WizardString(f, "model", "")); v != "" {
				entry.model = v
			}
			return nil
		},
	})

	return steps
}

// bedrockModelFormSpec fetches the live Bedrock model list (with a 45 s
// timeout) and returns it as a select form. Falls back to a hardcoded list
// when discovery fails. Matches the runtime behaviour of providerSteps()
// in onboard_steps.go.
func bedrockModelFormSpec(entry *provEntry) tuibridge.WizardFormSpec {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	picks := bedrock.ListOnboardingTextModels(ctx, bedrock.Config{
		Region:          strings.TrimSpace(entry.awsRegion),
		Profile:         strings.TrimSpace(entry.awsProfile),
		AccessKeyID:     strings.TrimSpace(entry.awsAccessKey),
		SecretAccessKey: strings.TrimSpace(entry.awsSecretKey),
		APIKey:          strings.TrimSpace(entry.apiKey),
	})
	var opts []tuibridge.WizardOptionSpec
	for _, p := range picks {
		opts = append(opts, tuibridge.WizardOptionSpec{Value: p.ID, Label: p.Label})
	}
	if len(opts) == 0 {
		opts = []tuibridge.WizardOptionSpec{
			{Value: "anthropic.claude-3-5-sonnet-20241022-v2:0", Label: "Claude 3.5 Sonnet v2"},
			{Value: "anthropic.claude-3-haiku-20240307-v1:0", Label: "Claude 3 Haiku"},
			{Value: "meta.llama3-1-70b-instruct-v1:0", Label: "Llama 3.1 70B Instruct"},
		}
	}
	opts = append(opts, tuibridge.WizardOptionSpec{Value: "custom", Label: "Other / Custom…"})
	return tuibridge.WizardFormSpec{
		Fields: []tuibridge.WizardFieldSpec{
			tuibridge.WizardSelect("model", "Select Bedrock Model",
				"Lists ON_DEMAND text models in your region.",
				entry.model, opts),
		},
	}
}
