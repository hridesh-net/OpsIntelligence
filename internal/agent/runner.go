// Package agent implements the OpsIntelligence agent runner — the main loop that
// routes messages to LLMs, dispatches tool calls, manages context, and
// writes to all three memory tiers.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/channels"
	"github.com/opsintelligence/opsintelligence/internal/localintel"
	"github.com/opsintelligence/opsintelligence/internal/memory"
	"github.com/opsintelligence/opsintelligence/internal/observability/correlation"
	"github.com/opsintelligence/opsintelligence/internal/observability/metrics"
	"github.com/opsintelligence/opsintelligence/internal/observability/runtrace"
	obstracing "github.com/opsintelligence/opsintelligence/internal/observability/tracing"
	"github.com/opsintelligence/opsintelligence/internal/provider"
	"github.com/opsintelligence/opsintelligence/internal/security"
	"github.com/opsintelligence/opsintelligence/internal/system"
)

// enterprisePosturePrompt is appended when Config.Enterprise is set (binary / high-load installs).
const enterprisePosturePrompt = `## Enterprise posture
- Decompose large goals into milestones, gather evidence with tools before conclusions, and use **chain_run** for packaged flows (pr-review, sonar-triage, …) once you have real inputs.
- Fan out independent work with **subagent_run** / **subagent_run_parallel** when it reduces wall-clock time; keep one coherent thread when work is tightly coupled.
- Prefer **devops.\*** and repository facts over guesses; stay **read-only** on GitHub/GitLab/Jenkins/Sonar unless the human clearly confirmed a write in the same turn.`

// ─────────────────────────────────────────────
// Tool interface
// ─────────────────────────────────────────────

// Tool is the interface that all built-in and user-generated tools must implement.
type Tool interface {
	// Definition returns the schema passed to the LLM.
	Definition() provider.ToolDef
	// Execute runs the tool with the given JSON input.
	// Returns (output string, error). Non-fatal errors should be returned
	// as output strings so the LLM can reason about them.
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolRegistry maps tool names to implementations.
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry creates an empty tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]Tool)}
}

// Register adds a tool.
func (r *ToolRegistry) Register(t Tool) {
	r.tools[t.Definition().Name] = t
}

// Get returns a tool by name.
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns all tool definitions for LLM requests.
func (r *ToolRegistry) Definitions() []provider.ToolDef {
	defs := make([]provider.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

// CloneWithout returns a new registry with the given tool names omitted (e.g. avoid sub-agent recursion).
func (r *ToolRegistry) CloneWithout(omit ...string) *ToolRegistry {
	skip := make(map[string]bool, len(omit))
	for _, n := range omit {
		skip[n] = true
	}
	out := NewToolRegistry()
	for _, t := range r.tools {
		n := t.Definition().Name
		if skip[n] {
			continue
		}
		out.Register(t)
	}
	return out
}

// ─────────────────────────────────────────────
// Runner
// ─────────────────────────────────────────────

// ToolCatalog is the interface the Runner uses to select per-request tools.
// Implemented by tools.Catalog. If nil, falls back to r.tools.Definitions().
type ToolCatalog interface {
	SelectForRequest(userMessage string, caps provider.ProviderCaps) []provider.ToolDef
	RecordUsage(toolName string)
	DecayInertia()
}

// Config holds runner-specific settings.
type Config struct {
	MaxIterations       int
	SystemPrompt        string
	Model               string
	ActiveSkillsContext string
	EnablePlanning      bool
	EnableReflection    bool
	// Enterprise opts into a compact system-prompt posture for high-load installs.
	Enterprise bool
	EmbeddingModel      string
	SessionID           string // The persistent session ID for this runner
	ChannelID           string // The message channel ID (e.g. "slack")
	ProviderName        string // Lowercase provider ID for capability detection
	ToolsProfile        string // "full" or "coding"
	// GatewayPublicBaseURL is e.g. http://127.0.0.1:18790 — used to tell users where /workspace/ files are served.
	GatewayPublicBaseURL string
	// ExtensionPromptAppend is optional text from opsintelligence.yaml extensions.prompt_files (markdown fragments).
	ExtensionPromptAppend string
	// StateDir is the OpsIntelligence state root (for owner-only policy path checks in the guardrail).
	StateDir string
	// RunTracePath is an optional absolute path to an append-only NDJSON trace file.
	RunTracePath string
	// RunnerRole is "master" (default) or "subagent"; recorded in run_trace.task_start.
	RunnerRole string
	// RunTraceMode is the resolved agent.run_trace_mode (e.g. auto, off) for run_trace.task_start.
	RunTraceMode string
	// EnabledSkillNames lists skills merged into this runner (for run_trace only).
	EnabledSkillNames []string
	Palace   PalaceConfig
	// LocalIntel runs optional on-device Gemma before the main model (see opsintelligence.yaml agent.local_intel).
	LocalIntel LocalIntelRunnerConfig
}

// LocalIntelRunnerConfig is the agent-local view of config.Agent.LocalIntel.
type LocalIntelRunnerConfig struct {
	Enabled      bool
	GGUFPath     string
	MaxTokens    int
	SystemPrompt string
	CacheDir     string
}

type PalaceConfig struct {
	Enabled             bool
	ShadowOnly          bool
	PromptRouting       bool
	MemorySearchRouting bool
	ToolRouting         bool
	FailOpen            bool
	LogDecisions        bool
}

// Runner is the main agent execution loop.
type Runner struct {
	cfg           Config
	provider      provider.Provider
	tools         *ToolRegistry
	catalog       ToolCatalog // graph-based per-request tool selector (optional)
	memory        *memory.Manager
	working       *memory.WorkingMemory   // Added working memory field
	mediaFn       channels.MediaReplyFunc // Callback for sending media
	sessionRunner bool                    // Flag for session-specific runner
	log           *zap.Logger

	// Security layer (optional; both may be nil)
	guardrail *security.Guardrail
	auditLog  *security.AuditLog
	hardware  *system.HardwareReport

	sessionID    string
	channelID    string
	workspaceDir string

	// modelRegistry is optional; used for channel slash commands like /models.
	modelRegistry *provider.Registry
	palaceRouter  memory.PalaceRouter

	commands map[string]func(ctx context.Context, replyFn channels.StreamingReplyFunc) error

	// localIntel* fields support optional Gemma pre-pass (same Runner = one opened engine).
	localIntelOnce    sync.Once
	localIntelEng     localintel.Engine
	localIntelOpenErr error
	localIntelScratch string // advisory text merged into buildSystemPrompt for the current Run/RunStream

	// systemPromptAugmentor, when non-nil, is invoked once per iteration
	// by buildSystemPrompt. Its return value is appended to the system
	// prompt under a clearly-delimited block, so callers can inject
	// per-turn context without coupling the runner to a specific source
	// (e.g. the master uses this for the sub-agent dashboard; children
	// use it to drain pending interventions from the task manager).
	// Return "" to skip appending on a given turn.
	systemPromptAugmentor func(ctx context.Context) string

	// traceLoopIteration is the current agent loop index (1-based) for run_trace tool_call/tool_done.
	traceLoopIteration int
}

// WithSystemPromptAugmentor installs a per-turn callback whose return value
// is appended to the system prompt (under a clearly-delimited block). Use
// this to inject dynamic ambient context that should not live in the
// base SystemPrompt — e.g. live sub-agent dashboards on a master runner,
// or freshly-drained intervention messages on a child runner.
//
// Returns r for chaining after NewRunner / WithSession.
func (r *Runner) WithSystemPromptAugmentor(fn func(ctx context.Context) string) *Runner {
	r.systemPromptAugmentor = fn
	return r
}

// NewRunner creates a new agent runner.
func NewRunner(
	cfg Config,
	p provider.Provider,
	tools *ToolRegistry,
	mem *memory.Manager,
	log *zap.Logger,
	workspaceDir string,
) *Runner {
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 64
	}
	sessionID := cfg.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	return &Runner{
		cfg:           cfg,
		provider:      p,
		tools:         tools,
		memory:        mem,
		working:       mem.GetWorking(sessionID),
		sessionRunner: false, // This is the base runner, not session-specific
		log:           log,
		sessionID:     sessionID,
		channelID:     cfg.ChannelID,
		workspaceDir:  workspaceDir,
		hardware:      &system.HardwareReport{},
		palaceRouter:  memory.NewHeuristicPalaceRouter(),
	}
}

// WithCatalog sets the graph-based tool catalog on an existing runner.
// Call this after NewRunner to enable per-request tool filtering.
func (r *Runner) WithCatalog(c ToolCatalog) *Runner {
	r.catalog = c
	return r
}

// WithHardware sets the hardware report on an existing runner.
func (r *Runner) WithHardware(h *system.HardwareReport) *Runner {
	if h != nil {
		r.hardware = h
	}
	return r
}

// WithSecurity attaches the guardrail and audit log to an existing runner.
// Both are optional — pass nil to disable either.
func (r *Runner) WithSecurity(g *security.Guardrail, a *security.AuditLog) *Runner {
	r.guardrail = g
	r.auditLog = a
	if a != nil {
		a.WriteSessionStart(r.sessionID, r.channelID)
	}
	return r
}

// WithModelRegistry attaches the LLM registry for slash commands (/models) on messaging channels.
func (r *Runner) WithModelRegistry(reg *provider.Registry) *Runner {
	r.modelRegistry = reg
	return r
}

// plan generates a multi-step execution plan for the given query.
func (r *Runner) plan(ctx context.Context, query string) (string, error) {
	prompt := fmt.Sprintf(`
You are in the **PLANNING PHASE**. Your goal is to break down the user's request into a series of logical, executable milestones.
Request: "%s"

Please provide:
1. A concise summary of the goal.
2. A numbered list of milestones.
3. Potential risks or edge cases to watch for.

Format your response inside <planning> tags. 
Do NOT call any tools yet. Just plan.
`, query)

	req := &provider.CompletionRequest{
		Model:        r.cfg.Model,
		SystemPrompt: r.buildSystemPrompt(ctx, query),
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, prompt),
		},
		MaxTokens: 2048,
	}

	resp, err := r.provider.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Text(), nil
}

// reflect critiques the agent's work against the original plan.
func (r *Runner) reflect(ctx context.Context, query string, plan string) (string, bool, error) {
	prompt := fmt.Sprintf(`
You are in the **REFLECTION PHASE**. Your goal is to critique your own work.
Original Request: "%s"
Original Plan:
%s

Review the conversation history above. Did you successfully achieve the goal? 
Provide:
1. **Self-Critique**: What went well? what failed?
2. **Status**: Return EXACTLY "SUCCESS" if the task is fully complete, or "RETRY" if more work is needed.
3. **Lesson Learned**: If something failed or was unexpectedly complex, provide a concise lesson for your future self inside <lesson_learned> tags.

Format your response inside <reflexion> tags.
`, query, plan)

	req := &provider.CompletionRequest{
		Model:        r.cfg.Model,
		SystemPrompt: r.buildSystemPrompt(ctx, query),
		Messages:     r.convertMessages(r.working.Messages()), // Use helper
		MaxTokens:    2048,
	}
	// Append the reflection prompt as the last user message
	req.Messages = append(req.Messages, provider.NewTextMessage(provider.RoleUser, prompt))

	resp, err := r.provider.Complete(ctx, req)
	if err != nil {
		return "", false, err
	}

	text := resp.Text()
	success := strings.Contains(text, "SUCCESS")
	return text, success, nil
}

// WithSession returns a new Runner clone for a specific session ID.
func (r *Runner) WithSession(sessionID string) *Runner {
	return &Runner{
		cfg:           r.cfg,
		provider:      r.provider,
		tools:         r.tools,
		catalog:       r.catalog,
		memory:        r.memory,
		working:       r.memory.GetWorking(sessionID),
		mediaFn:       r.mediaFn,
		hardware:      r.hardware,
		guardrail:     r.guardrail,
		auditLog:      r.auditLog,
		commands:      r.commands,
		sessionRunner: true,
		log:           r.log,
		sessionID:     sessionID,
		channelID:     r.channelID,
		workspaceDir:  r.workspaceDir,
		modelRegistry:         r.modelRegistry,
		palaceRouter:          r.palaceRouter,
		systemPromptAugmentor: r.systemPromptAugmentor,
	}
}

// SessionID returns the current session ID.
func (r *Runner) SessionID() string { return r.sessionID }

func (r *Runner) logFields(ctx context.Context, extra ...zap.Field) []zap.Field {
	fields := correlation.Fields(ctx)
	if correlation.SessionID(ctx) == "" && r.sessionID != "" {
		fields = append(fields, zap.String("session_id", r.sessionID))
	}
	if correlation.Channel(ctx) == "" && r.channelID != "" {
		fields = append(fields, zap.String("channel", r.channelID))
	}
	return append(fields, extra...)
}

// traceRoutingCatalog is implemented by tools.Catalog for run_trace routing hints.
type traceRoutingCatalog interface {
	TraceRoutingIntents(userMessage string) []string
}

func (r *Runner) tracePath(ctx context.Context) string {
	if p := runtrace.OutputPathFrom(ctx); p != "" {
		return p
	}
	return strings.TrimSpace(r.cfg.RunTracePath)
}

func (r *Runner) routingIntentsForTrace(userMessage string) []string {
	if r.catalog == nil {
		return nil
	}
	c, ok := r.catalog.(traceRoutingCatalog)
	if !ok {
		return nil
	}
	return c.TraceRoutingIntents(userMessage)
}

func (r *Runner) emitTrace(ctx context.Context, kind string, fields map[string]any) {
	path := r.tracePath(ctx)
	if path == "" {
		return
	}
	ev := map[string]any{}
	for k, v := range fields {
		ev[k] = v
	}
	ev["kind"] = kind
	if id := correlation.RequestID(ctx); id != "" {
		ev["request_id"] = id
	}
	if id := correlation.SessionID(ctx); id != "" {
		ev["session_id"] = id
	} else if r.sessionID != "" {
		ev["session_id"] = r.sessionID
	}
	if ch := correlation.Channel(ctx); ch != "" {
		ev["channel"] = ch
	} else if r.channelID != "" {
		ev["channel"] = r.channelID
	}
	runtrace.Append(path, ev)
}

func (r *Runner) emitTraceTaskDone(ctx context.Context, iterations int, finish string, errMsg string) {
	m := map[string]any{
		"iterations": iterations,
		"finish":     finish,
	}
	if errMsg != "" {
		m["error"] = truncateForTrace(errMsg, 500)
	}
	r.emitTrace(ctx, "task_done", m)
}

func (r *Runner) traceTaskStart(ctx context.Context, userMessage string) {
	if r.tracePath(ctx) == "" {
		return
	}
	advisory := strings.TrimSpace(r.localIntelScratch) != ""
	role := strings.TrimSpace(r.cfg.RunnerRole)
	if role == "" {
		role = "master"
	}
	traceFields := map[string]any{
		"query_preview":          truncateForTrace(userMessage, 240),
		"runner_role":            role,
		"routing_intents":        r.routingIntentsForTrace(userMessage),
		"skills_context_chars":   len(strings.TrimSpace(r.cfg.ActiveSkillsContext)),
		"skills_enabled":         append([]string(nil), r.cfg.EnabledSkillNames...),
		"skills_enabled_count":   len(r.cfg.EnabledSkillNames),
		"primary_model":          r.cfg.Model,
		"llm_backend":            runtrace.InferBackend(r.cfg.ProviderName, r.cfg.Model, r.cfg.LocalIntel.Enabled, advisory),
		"tools_profile":          r.cfg.ToolsProfile,
		"provider":               r.cfg.ProviderName,
		"local_intel_enabled":    r.cfg.LocalIntel.Enabled,
		"local_advisory_applied": advisory,
	}
	if m := strings.TrimSpace(r.cfg.RunTraceMode); m != "" {
		traceFields["run_trace_mode"] = m
	}
	r.emitTrace(ctx, "task_start", traceFields)
}

func (r *Runner) traceModelIteration(ctx context.Context, iteration int, userMessage string, req *provider.CompletionRequest) {
	if r.tracePath(ctx) == "" || req == nil {
		return
	}
	names := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		names = append(names, t.Name)
	}
	advisory := strings.TrimSpace(r.localIntelScratch) != ""
	r.emitTrace(ctx, "model_iteration", map[string]any{
		"iteration":            iteration,
		"model":                req.Model,
		"llm_backend":          runtrace.InferBackend(r.cfg.ProviderName, req.Model, r.cfg.LocalIntel.Enabled, advisory),
		"routing_intents":      r.routingIntentsForTrace(userMessage),
		"skills_context_chars": len(strings.TrimSpace(r.cfg.ActiveSkillsContext)),
		"tools_offered":        names,
	})
}

func truncateForTrace(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// streamInternalAgentUI is true when we should show planning / reflection / maintenance
// tokens to the stream handler (CLI, web SSE). Messaging apps must not surface this.
func (r *Runner) streamInternalAgentUI() bool {
	return r.channelID == ""
}

func (r *Runner) streamThinking(handler StreamHandler, s string) {
	if !r.streamInternalAgentUI() {
		return
	}
	if s != "" {
		handler.OnToken(s)
	}
}

// RunResult holds the outcome of a Run call.
type RunResult struct {
	SessionID  string
	Response   string
	Iterations int
	Usage      provider.TokenUsage
}

// Run processes a user message and returns the assistant's final response.
// It handles the complete tool-use loop: LLM → tool calls → tool results → LLM.
func (r *Runner) Run(ctx context.Context, msg memory.Message) (*RunResult, error) {
	ctx, _ = correlation.EnsureRequestID(ctx)
	ctx = correlation.WithSessionID(ctx, r.sessionID)
	ctx = correlation.WithChannel(ctx, r.channelID)
	if p := strings.TrimSpace(r.cfg.RunTracePath); p != "" {
		ctx = runtrace.WithOutputPath(ctx, p)
	}
	ctx, enqueueSpan := obstracing.StartSpan(ctx, "agent.enqueue_message")

	userMessage := msg.Content
	// Append user message to working memory.
	userMsg := msg
	if userMsg.ID == "" {
		userMsg.ID = uuid.New().String()
	}
	if userMsg.CreatedAt.IsZero() {
		userMsg.CreatedAt = time.Now()
	}
	r.working.Append(userMsg)
	if err := r.memory.Episodic.Save(ctx, userMsg); err != nil {
		r.log.Warn("episodic save failed", zap.Error(err))
	}
	enqueueSpan.End()
	defer func() { r.localIntelScratch = "" }()

	var totalUsage provider.TokenUsage
	iterations := 0

	r.prepareLocalIntelScratch(ctx, userMessage)
	r.traceTaskStart(ctx, userMessage)

	// V3: Planning Phase
	plan := ""
	if r.cfg.EnablePlanning {
		r.log.Info("entering planning phase")
		var err error
		plan, err = r.plan(ctx, userMessage)
		if err != nil {
			r.log.Warn("planning failed", zap.Error(err))
		} else {
			// Add plan to working memory
			planMsg := memory.Message{
				ID: uuid.New().String(), SessionID: r.sessionID, Role: memory.RoleSystem,
				Content: "[PLAN]\n" + plan, CreatedAt: time.Now(),
			}
			r.working.Append(planMsg)
		}
	}

	// Check if we need to flush memory before starting.
	// We'll perform one "proactive flush turn" if budget is tight.
	if r.shouldFlush() {
		r.doFlush(ctx, &totalUsage)
	}

	for iterations < r.cfg.MaxIterations {
		iterations++
		r.traceLoopIteration = iterations

		// Build the completion request from working memory.
		req := r.buildRequestV3(ctx, userMessage)
		r.traceModelIteration(ctx, iterations, userMessage, req)

		r.log.Debug("running completion",
			zap.String("model", r.cfg.Model),
			zap.Int("messages", len(req.Messages)),
			zap.Int("iteration", iterations),
		)

		// Stream the response.
		modelCtx, modelSpan := obstracing.StartSpan(ctx, "agent.model_call")
		stream, err := r.provider.Stream(modelCtx, req)
		if err != nil {
			modelSpan.End()
			r.emitTraceTaskDone(ctx, iterations, "error", err.Error())
			return nil, fmt.Errorf("agent: stream: %w", err)
		}

		resp, err := provider.CollectStream(modelCtx, stream)
		modelSpan.End()
		if err != nil {
			r.emitTraceTaskDone(ctx, iterations, "error", err.Error())
			return nil, fmt.Errorf("agent: collect stream: %w", err)
		}

		totalUsage.PromptTokens += resp.Usage.PromptTokens
		totalUsage.CompletionTokens += resp.Usage.CompletionTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens

		assistantContent := resp.Text()
		toolCalls := resp.ToolCalls()

		if len(toolCalls) == 0 {
			if x := extractXMLFunctionCalls(assistantContent); len(x) > 0 {
				toolCalls = x
				assistantContent = stripXMLFunctionBlocks(assistantContent)
			}
		}
		if len(toolCalls) == 0 {
			toolCalls = extractMarkdownBash(assistantContent)
		}
		r.normalizeToolCallNames(toolCalls)

		if strings.TrimSpace(assistantContent) == "" && len(toolCalls) > 0 {
			assistantContent = "[Activating tools...]"
		}

		assistantMsg := memory.Message{
			ID:        uuid.New().String(),
			SessionID: r.sessionID,
			Role:      memory.RoleAssistant,
			Content:   assistantContent,
			Parts:     toolCalls,
			Model:     r.cfg.Model,
			Tokens:    resp.Usage.CompletionTokens,
			CreatedAt: time.Now(),
		}
		r.working.Append(assistantMsg)
		if err := r.memory.Episodic.Save(ctx, assistantMsg); err != nil {
			r.log.Warn("episodic save failed", zap.Error(err))
		}

		// If no tool calls, we're done (do not use FinishReasonStop alone — models may emit
		// stop while only pseudo-XML tool blocks were parsed above).
		if len(toolCalls) == 0 {
			// Compact working memory if over budget.
			r.working.Compact(r.working.TotalTokens())

			r.emitTraceTaskDone(ctx, iterations, "stop", "")
			return &RunResult{
				SessionID:  r.sessionID,
				Response:   assistantContent,
				Iterations: iterations,
				Usage:      totalUsage,
			}, nil
		}

		// Execute tool calls and collect results.
		for _, tc := range toolCalls {
			result := r.executeTool(ctx, tc)

			toolResultMsg := memory.Message{
				ID:        uuid.New().String(),
				SessionID: r.sessionID,
				Role:      memory.RoleTool,
				Content:   result,
				Parts: []provider.ContentPart{
					{
						Type:              provider.ContentTypeToolResult,
						ToolResultID:      tc.ToolUseID,
						ToolResultContent: result,
					},
				},
				CreatedAt: time.Now(),
			}
			r.working.Append(toolResultMsg)
			if err := r.memory.Episodic.Save(ctx, toolResultMsg); err != nil {
				r.log.Warn("episodic save failed", zap.Error(err))
			}
		}
	}

	// V3: Reflection Phase
	if r.cfg.EnableReflection {
		r.log.Info("entering reflection phase")
		critique, success, err := r.reflect(ctx, userMessage, plan)
		if err == nil {
			if !success && iterations < r.cfg.MaxIterations {
				r.log.Info("self-critique requested retry", zap.String("critique", critique))
				// Optionally append critique and keep going, but for now we just return
			}

			// Store any lessons learned
			if strings.Contains(critique, "<lesson_learned>") && r.cfg.EmbeddingModel != "" {
				lessonText := extractTag(critique, "lesson_learned")
				if lessonText != "" {
					emb, _ := r.provider.Embed(ctx, r.cfg.EmbeddingModel, userMessage)
					_ = r.memory.Semantic.SaveLesson(ctx, memory.Lesson{
						ID: uuid.New().String(), Query: userMessage, Insights: lessonText,
						Success: success, Embedding: emb, CreatedAt: time.Now(),
					})
				}
			}
		}
	}

	r.emitTraceTaskDone(ctx, iterations, "max_iterations", "")
	return nil, fmt.Errorf("agent: exceeded max iterations (%d)", r.cfg.MaxIterations)
}

func extractTag(text, tag string) string {
	startTag := "<" + tag + ">"
	endTag := "</" + tag + ">"
	start := strings.Index(text, startTag)
	if start == -1 {
		return ""
	}
	end := strings.Index(text, endTag)
	if end == -1 || end <= start+len(startTag) {
		return ""
	}
	return text[start+len(startTag) : end]
}

// extractMarkdownBash finds ```bash or ```sh blocks in the model's plaintext
// and wraps them into synthetic tool calls if the model failed to use the JSON schema.
func extractMarkdownBash(text string) []provider.ContentPart {
	var results []provider.ContentPart
	re := regexp.MustCompile(`(?s)\x60\x60\x60(?:bash|sh)\n(.*?)\x60\x60\x60`)
	matches := re.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		code := strings.TrimSpace(m[1])
		if code == "" {
			continue
		}
		inputMap := map[string]any{"command": code}
		results = append(results, provider.ContentPart{
			Type:      provider.ContentTypeToolUse,
			ToolUseID: "call_md_" + uuid.New().String()[:8],
			ToolName:  "bash",
			ToolInput: inputMap,
		})
	}
	return results
}

// xmlFunctionOuterRe matches pseudo-tool blocks some models emit instead of native tool JSON.
// Example: <function=devops.github.list_prs><parameter=owner>EvoMap</parameter>...</function>
var xmlFunctionOuterRe = regexp.MustCompile(`(?is)<function=([a-zA-Z0-9_.-]+)\s*>(.*?)</function>`)

// xmlParameterRe captures <parameter=name>value</parameter> inside a function block.
var xmlParameterRe = regexp.MustCompile(`(?is)<parameter=([a-zA-Z0-9_]+)\s*>(.*?)</parameter>`)

func extractXMLFunctionCalls(text string) []provider.ContentPart {
	var results []provider.ContentPart
	for _, outer := range xmlFunctionOuterRe.FindAllStringSubmatch(text, -1) {
		if len(outer) < 3 {
			continue
		}
		toolName := strings.TrimSpace(outer[1])
		if toolName == "" {
			continue
		}
		inner := outer[2]
		args := map[string]any{}
		for _, pm := range xmlParameterRe.FindAllStringSubmatch(inner, -1) {
			if len(pm) < 3 {
				continue
			}
			key := strings.TrimSpace(pm[1])
			val := strings.TrimSpace(pm[2])
			if key != "" {
				args[key] = val
			}
		}
		results = append(results, provider.ContentPart{
			Type:      provider.ContentTypeToolUse,
			ToolUseID: "call_xml_" + uuid.New().String()[:12],
			ToolName:  toolName,
			ToolInput: args,
		})
	}
	return results
}

func stripXMLFunctionBlocks(text string) string {
	s := xmlFunctionOuterRe.ReplaceAllString(text, "")
	s = strings.ReplaceAll(s, "</tool_call>", "")
	return strings.TrimSpace(s)
}

// executeTool runs a single tool call and returns the result string.
func (r *Runner) executeTool(ctx context.Context, tc provider.ContentPart) (result string) {
	resolved := r.resolveCatalogToolName(tc.ToolName)
	inputJSON, err := json.Marshal(tc.ToolInput)
	if err != nil {
		result = fmt.Sprintf("Error marshalling tool input: %v", err)
		return result
	}

	start := time.Now()
	defer func() {
		if r.tracePath(ctx) == "" {
			return
		}
		ok := result != "" && !strings.HasPrefix(result, "Error") && !strings.HasPrefix(result, "[Security]")
		m := map[string]any{
			"iteration":    r.traceLoopIteration,
			"tool":         resolved,
			"ms":           time.Since(start).Milliseconds(),
			"ok":           ok,
			"result_chars": len(result),
		}
		if resolved != tc.ToolName && tc.ToolName != "" {
			m["requested_tool"] = tc.ToolName
		}
		r.emitTrace(ctx, "tool_done", m)
	}()

	if r.tracePath(ctx) != "" {
		m := map[string]any{
			"iteration":   r.traceLoopIteration,
			"tool":        resolved,
			"input_bytes": len(inputJSON),
		}
		if resolved != tc.ToolName && tc.ToolName != "" {
			m["requested_tool"] = tc.ToolName
		}
		r.emitTrace(ctx, "tool_call", m)
	}

	tool, ok := r.tools.Get(resolved)
	if !ok {
		r.log.Warn("tool not found", zap.String("tool", resolved), zap.String("requested_tool", tc.ToolName))
		result = fmt.Sprintf("Error: tool %q not found", resolved)
		return result
	}

	// ── Guardrail: pre-execution tool check ──────────────────────────────
	if r.guardrail != nil {
		stateDir := r.cfg.StateDir
		if stateDir == "" {
			stateDir = r.workspaceDir
		}
		check := r.guardrail.CheckToolCall(resolved, string(inputJSON), stateDir, r.workspaceDir)
		if r.auditLog != nil && len(check.Findings) > 0 {
			r.auditLog.WriteGuardrailEvent(r.sessionID, r.channelID, check)
		}
		if check.Blocked() {
			r.log.Warn("guardrail blocked tool call",
				r.logFields(ctx,
					zap.String("tool", resolved),
					zap.String("reason", check.Message),
				)...,
			)
			result = "[Security] " + check.Message
			return result
		}
		if check.Action == security.ActionWarn {
			r.log.Warn("guardrail warning on tool call",
				r.logFields(ctx,
					zap.String("tool", resolved),
					zap.String("warning", check.Message),
				)...,
			)
		}
	}

	r.log.Info("tool call",
		r.logFields(ctx,
			zap.String("tool", resolved),
			zap.String("input", truncate(string(inputJSON), 200)),
		)...,
	)

	// Record tool usage for session inertia (boosts graph neighbours next turn).
	if r.catalog != nil {
		r.catalog.RecordUsage(resolved)
	}

	t0 := time.Now()
	if r.mediaFn != nil {
		ctx = context.WithValue(ctx, channels.MediaFnKey, r.mediaFn)
	}
	ctx = correlation.WithTraceLoopIteration(ctx, r.traceLoopIteration)
	var execErr error
	result, execErr = tool.Execute(ctx, inputJSON)
	dur := time.Since(t0)
	if execErr != nil {
		r.log.Error("tool execution failed",
			r.logFields(ctx,
				zap.String("tool", resolved),
				zap.Error(execErr),
			)...,
		)
		result = fmt.Sprintf("Error: %v", execErr)
		return result
	}

	if strings.TrimSpace(result) == "" {
		result = "Command executed successfully with no output."
	}

	// ── Audit log: record tool call event ────────────────────────────────
	if r.auditLog != nil {
		r.auditLog.WriteToolCall(
			r.sessionID, r.channelID, "", // actor resolved by channel layer
			resolved, inputJSON, result, dur,
			security.CheckResult{Action: security.ActionAllow},
		)
	}

	r.log.Info("tool result",
		r.logFields(ctx,
			zap.String("tool", resolved),
			zap.String("result", truncate(result, 200)),
		)...,
	)
	return result
}

// buildRequest converts working memory messages to a provider request.
func (r *Runner) convertMessages(msgs []memory.Message) []provider.Message {
	providerMsgs := make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		role := provider.Role(m.Role)
		content := m.Parts
		if len(content) == 0 {
			content = []provider.ContentPart{
				{Type: provider.ContentTypeText, Text: m.Content},
			}
		} else if m.Content != "" {
			// Prepend the raw text if parts exist (like tool calls)
			content = append([]provider.ContentPart{{Type: provider.ContentTypeText, Text: m.Content}}, content...)
		}
		providerMsgs = append(providerMsgs, provider.Message{
			Role:    role,
			Content: content,
		})
	}
	return providerMsgs
}

func (r *Runner) buildRequestV3(ctx context.Context, query string) *provider.CompletionRequest {
	if r.cfg.Palace.Enabled && r.cfg.Palace.LogDecisions && r.palaceRouter != nil {
		route := r.palaceRouter.Route(query)
		r.log.Debug("palace route decision",
			zap.String("palace", route.Palace),
			zap.String("wing", route.Wing),
			zap.String("room", route.Room),
			zap.Float64("confidence", route.Confidence),
			zap.Bool("shadow_only", r.cfg.Palace.ShadowOnly),
		)
	}
	base := r.filterTools(r.selectTools(query))
	tools := r.mergeToolsNeededForHistory(base)
	return &provider.CompletionRequest{
		Model:        r.cfg.Model,
		Messages:     r.convertMessages(r.working.Messages()),
		SystemPrompt: r.buildSystemPrompt(ctx, query),
		Tools:        tools,
		MaxTokens:    8096,
		Stream:       true,
	}
}

// mergeToolsNeededForHistory adds tool definitions for names that appear in working-memory
// tool_use turns but were dropped from the current graph selection. This keeps Bedrock's
// alias map aligned with conversation history (avoids unmapped devops_github_* names).
func (r *Runner) mergeToolsNeededForHistory(selected []provider.ToolDef) []provider.ToolDef {
	if r.tools == nil {
		return selected
	}
	have := make(map[string]struct{}, len(selected)+8)
	for _, d := range selected {
		have[d.Name] = struct{}{}
	}
	out := append([]provider.ToolDef(nil), selected...)
	for _, m := range r.working.Messages() {
		for _, p := range m.Parts {
			if p.Type != provider.ContentTypeToolUse {
				continue
			}
			name := r.resolveCatalogToolName(strings.TrimSpace(p.ToolName))
			if name == "" {
				continue
			}
			if _, ok := have[name]; ok {
				continue
			}
			t, ok := r.tools.Get(name)
			if !ok {
				continue
			}
			out = append(out, t.Definition())
			have[name] = struct{}{}
		}
	}
	return out
}

func (r *Runner) selectTools(query string) []provider.ToolDef {
	var target []provider.ToolDef
	if r.catalog != nil {
		caps := provider.CapsFor(r.cfg.ProviderName)
		// Decay inertia from last turn before computing new selection
		r.catalog.DecayInertia()
		target = r.catalog.SelectForRequest(query, caps)
	} else {
		target = r.tools.Definitions() // backward-compat fallback
	}
	return r.filterTools(target)
}

func (r *Runner) filterTools(defs []provider.ToolDef) []provider.ToolDef {
	if r.cfg.ToolsProfile != "coding" {
		return defs
	}
	unsafe := map[string]bool{
		"browser_navigate":   true,
		"browser_screenshot": true,
		"web_search":         true,
		"web_fetch":          true,
		"message":            true,
		"cron":               true,
		"image_understand":   true,
	}
	var filtered []provider.ToolDef
	for _, d := range defs {
		if !unsafe[d.Name] {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// buildRequest converts working memory messages to a provider request.
func (r *Runner) buildRequest() *provider.CompletionRequest {
	return &provider.CompletionRequest{
		Model:        r.cfg.Model,
		Messages:     r.convertMessages(r.working.Messages()),
		SystemPrompt: r.buildSystemPrompt(context.TODO(), ""), // default/empty
		Tools:        r.tools.Definitions(),
		MaxTokens:    8096,
		Stream:       true,
	}
}

func (r *Runner) buildSystemPrompt(ctx context.Context, query string) string {
	today := time.Now().Format("2006-01-02")
	ws := r.workspaceDir

	// ── Hardware Environment ────────────────────────────────────────────────
	hwStr := ""
	if r.hardware != nil {
		hw := r.hardware
		hwStr += "\n## Hardware Environment\n"
		if len(hw.Cameras) > 0 {
			hwStr += fmt.Sprintf("- Cameras: %s\n", strings.Join(hw.Cameras, ", "))
		}
		if len(hw.AudioDevices) > 0 {
			hwStr += fmt.Sprintf("- Audio: %s\n", strings.Join(hw.AudioDevices, ", "))
		}
		if len(hw.InputDevices) > 0 {
			hwStr += fmt.Sprintf("- Input Devices: %s\n", strings.Join(hw.InputDevices, ", "))
		}
	}

	// ── Workspace Identity (markdown persona files) ─────────────────────────
	// SOUL.md is the source of truth for core behavior/capabilities.
	readWS := func(name string) (string, bool) {
		data, err := os.ReadFile(filepath.Join(ws, name))
		if err != nil {
			return "", false
		}
		content := strings.TrimSpace(string(data))
		if content == "" || looksLikeTemplate(content) {
			return "", false
		}
		return content, true
	}
	soulContent, hasSoul := readWS("SOUL.md")
	identityContent, hasIdentity := readWS("IDENTITY.md")
	userContent, hasUser := readWS("USER.md")
	agentsContent, hasAgents := readWS("AGENTS.md")
	bootstrapContent, hasBootstrap := readWS("BOOTSTRAP.md")
	toolsContent, hasTools := readWS("TOOLS.md")

	personaFromWorkspace := ""
	if hasSoul {
		personaFromWorkspace += fmt.Sprintf("\n## Agent Soul / Persona (Source of Truth)\n%s\n", soulContent)
	}
	if hasIdentity {
		personaFromWorkspace += fmt.Sprintf("\n## Identity\n%s\n", identityContent)
	}
	if hasUser {
		personaFromWorkspace += fmt.Sprintf("\n## User Context\n%s\n", userContent)
	}
	if hasAgents {
		personaFromWorkspace += fmt.Sprintf("\n## Agent Rules\n%s\n", agentsContent)
	}
	if hasBootstrap {
		personaFromWorkspace += fmt.Sprintf("\n## Bootstrap Instructions\n%s\n", bootstrapContent)
	}
	if hasTools {
		personaFromWorkspace += fmt.Sprintf("\n## Tool Preferences\n%s\n", toolsContent)
	}

	// ── Dynamic tool table — built from the live ToolRegistry ────────────────
	// This means every newly registered tool (web_search, edit, process, etc.)
	// is automatically surfaced to the LLM without manual edits here.
	toolTable := r.buildToolTable()

	// ── Core identity ────────────────────────────────────────────────────────
	// If workspace identity files exist, they ARE the identity.
	// SOUL.md is authoritative for core behavior/capabilities.
	// The hardcoded block is the fallback for bare installs with no workspace files.
	var identityBlock string
	if personaFromWorkspace != "" {
		identityBlock = personaFromWorkspace
	} else {
		identityBlock = `You are **OpsIntelligence** — an autonomous DevOps agent. Your main jobs are **PR review**, **pipelines/CI**, **Sonar**, **incidents**, and **runbooks**. Pull facts with tools (especially devops.*), answer in plain language, stay **read-only** on GitHub/GitLab/Jenkins/Sonar/Slack unless the human clearly confirms a write in the same turn. Follow team Markdown under teams/* and owner policies under POLICIES.md / policies/ in the state directory.`
	}

	base := identityBlock + `

` + hwStr + `

## Available tools

` + toolTable + `

## How you work
- **Ship outcomes, not homework:** when someone asks for something, **do it** with tools. Do not hand them long shell blocks to run—you use ` + "`bash`" + `, file tools, and ` + "`devops.*`" + `.
- **PR review:** smart chains only see text you give them. For a GitHub PR URL: parse owner/repo/number → ` + "`devops.github.pull_request`" + ` then ` + "`devops.github.pr_diff`" + ` (trim diff ~24k) → optional CI/status tools → ` + "`chain_run`" + ` with ` + "`id\":\"pr-review`" + ` and inputs ` + "`github_pr_json`" + `, ` + "`github_diff`" + `, etc. Use ` + "`chain_list`" + ` if you need chain ids.
- **Other flows:** ` + "`chain_run`" + ` for ` + "`sonar-triage`" + `, ` + "`cicd-regression`" + `, ` + "`incident-scribe`" + ` when they match the task—still gather evidence with tools first when the chain needs real data.
- **Tone:** verdict or status first, then short evidence and links. No filler, no narrating hidden startup phases.

## Policies
- **SOUL.md / IDENTITY.md / USER.md** in the state directory (when present) define name and team context. This runtime is always OpsIntelligence.
- **Owner-only:** never create or overwrite ` + "`POLICIES.md`" + `, ` + "`RULES.md`" + `, or files under ` + "`policies/`" + ` with tools.

## Remember preferences
- Update **IDENTITY.md** and **USER.md** at the state root in the same turn when the human agrees a name or preference—chat alone does not survive restart.
- Avatars / shareable HTML: **workspace/public/** (gateway serves **/workspace/...** when running).

## Fresh facts
- ` + "`web_search`" + ` to discover or verify current information; ` + "`web_fetch`" + ` when you already have the URL. For time-sensitive or version-specific questions, use the web tools—do not guess from training data alone.

## Workspace layout
Root: ` + ws + `
- Persona & rules: ` + ws + `/SOUL.md, ` + ws + `/IDENTITY.md, ` + ws + `/USER.md, ` + ws + `/AGENTS.md
- Memory: ` + ws + `/MEMORY.md, daily ` + ws + `/memory/` + today + `.md
- Public HTTP: ` + ws + `/workspace/public/

### Other one-offs
- Research → ` + "`web_search`" + ` then ` + "`web_fetch`" + `. Files → ` + "`write_file`" + ` / ` + "`edit`" + `. Commands → ` + "`bash`" + `. Search repo → ` + "`grep`" + `. Background server → ` + "`process`" + `. Schedules → ` + "`cron`" + `.`

	var dashboardHint string
	if r.cfg.GatewayPublicBaseURL != "" {
		dashboardHint = fmt.Sprintf(`

## Local dashboards (monitoring, status pages)
When the user asks for a **dashboard**, **status page**, or **live monitor**:
1. Write files under **%[1]s/workspace/public/** (for example **%[1]s/workspace/public/monitor/index.html**).
2. With **opsintelligence start** (background daemon / gateway) running, the user opens them at **%[2]s/workspace/<path-inside-public>** (example: **%[2]s/workspace/monitor/index.html**).
3. Use **meta refresh** or **JavaScript** polling to refresh; you can emit JSON via the **bash** tool for charts to poll.
4. After creating files, **give the user the full URL** in your reply.
5. **Never put API keys, tokens, or private data** in **workspace/public/** - that tree is served without the gateway API token so browsers can open links directly.`,
			ws, r.cfg.GatewayPublicBaseURL)
	}

	var parts []string
	parts = append(parts, base+dashboardHint)

	if ch := strings.TrimSpace(r.channelID); ch != "" {
		var chExtra string
		switch strings.ToLower(ch) {
		case "slack":
			chExtra = "\n- **Slack:** short replies, mrkdwn, small snippets—link out to PRs/pipelines for detail."
		case "whatsapp":
			chExtra = "\n- **WhatsApp:** short paragraphs; avoid dumping huge logs or raw diffs in one message."
		}
		parts = append(parts, `## Messages on this channel
- Write what the human should read: clear language only. Do **not** paste internal scaffolding (`+"`<planning>`"+`, `+"`<function=`"+`, long XML tool dumps, or giant checklists) into the user-visible reply.`+chExtra)
	}

	if r.cfg.SystemPrompt != "" {
		parts = append(parts, r.cfg.SystemPrompt)
	}

	// Corrective Memory (Lessons Learned from past tasks)
	if query != "" && r.cfg.EmbeddingModel != "" {
		emb, err := r.provider.Embed(ctx, r.cfg.EmbeddingModel, query)
		if err == nil {
			lessons, err := r.memory.Semantic.SearchLessons(ctx, emb, 3)
			if err == nil && len(lessons) > 0 {
				if r.cfg.Palace.Enabled && !r.cfg.Palace.ShadowOnly && r.cfg.Palace.PromptRouting && r.palaceRouter != nil {
					route := r.palaceRouter.Route(query)
					if !route.IsZero() {
						var filtered []memory.Lesson
						for _, l := range lessons {
							ll := strings.ToLower(l.Query + " " + l.Insights)
							if (route.Wing != "" && strings.Contains(ll, strings.ToLower(route.Wing))) ||
								(route.Room != "" && strings.Contains(ll, strings.ToLower(route.Room))) ||
								(route.Palace != "" && strings.Contains(ll, strings.ToLower(route.Palace))) {
								filtered = append(filtered, l)
							}
						}
						if len(filtered) > 0 {
							lessons = filtered
						} else if !r.cfg.Palace.FailOpen {
							lessons = nil
						}
					}
				}
				var sb strings.Builder
				sb.WriteString("\n## Past Task Insights\n")
				for _, l := range lessons {
					sb.WriteString(fmt.Sprintf("- Task: %s\n  Insight: %s\n", l.Query, l.Insights))
				}
				parts = append(parts, sb.String())
			}
		}
	}

	if r.cfg.ActiveSkillsContext != "" {
		parts = append(parts, r.cfg.ActiveSkillsContext)
	}

	if strings.TrimSpace(r.cfg.ExtensionPromptAppend) != "" {
		parts = append(parts, strings.TrimSpace(r.cfg.ExtensionPromptAppend))
	}

	if r.cfg.Enterprise {
		parts = append(parts, enterprisePosturePrompt)
	}

	if strings.TrimSpace(r.localIntelScratch) != "" {
		parts = append(parts, "## On-device advisory (local Gemma)\n"+strings.TrimSpace(r.localIntelScratch))
	}

	// Dynamic per-turn augmentation (see WithSystemPromptAugmentor).
	// The master runner uses this to inject a live sub-agent dashboard;
	// child runners use it to pull pending interventions from the task
	// manager at the top of every iteration. Return "" to skip.
	if r.systemPromptAugmentor != nil {
		if extra := strings.TrimSpace(r.systemPromptAugmentor(ctx)); extra != "" {
			parts = append(parts, extra)
		}
	}

	return strings.Join(parts, "\n\n")
}

// looksLikeTemplate detects untouched template markdown so we don't inject
// bootstrap scaffolding into the live system prompt.
func looksLikeTemplate(content string) bool {
	lc := strings.ToLower(content)
	if strings.Contains(lc, `title: "identity template"`) ||
		strings.Contains(lc, `title: "soul.md template"`) ||
		strings.Contains(lc, `title: "user template"`) ||
		strings.Contains(lc, `title: "agents.md template"`) {
		return true
	}
	templateMarkers := []string{
		"_fill this in during your first conversation.",
		"_you're not a chatbot. you're becoming someone._",
		"_learn about the person you're helping. update this as you go._",
		"if `bootstrap.md` exists, that's your birth certificate.",
	}
	hits := 0
	for _, marker := range templateMarkers {
		if strings.Contains(lc, marker) {
			hits++
		}
	}
	return hits >= 2
}

// buildToolTable generates a markdown table of all registered tools for the system prompt.
// Called dynamically so new tools are automatically surfaced to the LLM.
func (r *Runner) buildToolTable() string {
	// Friendly one-line descriptions for the table (supplements the schema description)
	friendlyDesc := map[string]string{
		"read_file":          "Read any file — source code, logs, configs",
		"write_file":         "Create or overwrite any file",
		"edit":               "Str-replace targeted edit (faster than write_file for single changes)",
		"apply_patch":        "Apply a unified diff (multi-hunk, multi-file edits)",
		"list_dir":           "Browse directories recursively",
		"grep":               "Regex pattern search across files",
		"bash":               "Run ANY shell command",
		"web_fetch":          "Fetch a specific URL and return its text content",
		"web_search":         "Search the web via DuckDuckGo — no API key needed",
		"memory_search":      "Semantic search over past conversations and indexed docs",
		"memory_get":         "Read specific lines from memory/indexed files",
		"browser_navigate":   "Open a URL in a real browser session",
		"browser_screenshot": "Capture a screenshot of the current browser page",
		"process":            "Start/stop/status/logs background processes (dev servers, watchers)",
		"env":                "Read/write .env files and OS environment variables",
		"image_understand":   "Analyze an image or screenshot with a vision model (OCR, UI review)",
		"sessions_list":      "List all past session IDs in episodic memory",
		"sessions_history":   "Read conversation history for a specific session",
		"cron":               "Schedule recurring shell commands (cron expressions)",
		"message":            "Proactively send a message to a connected channel (Slack, REST/WS gateway).",
	}

	defs := r.filterTools(r.tools.Definitions())
	if len(defs) == 0 {
		return "_No tools registered._"
	}

	var sb strings.Builder
	sb.WriteString("| Tool | Description |\n")
	sb.WriteString("|------|-------------|\n")
	for _, def := range defs {
		desc := def.Description
		if friendly, ok := friendlyDesc[def.Name]; ok {
			desc = friendly
		} else if idx := strings.Index(desc, "\n"); idx > 0 {
			desc = desc[:idx] // first line only
		}
		if len(desc) > 120 {
			desc = desc[:117] + "…"
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %s |\n", def.Name, desc))
	}
	return sb.String()
}

// ─────────────────────────────────────────────
// Stream runner (for interactive CLI use)
// ─────────────────────────────────────────────

// StreamHandler receives streaming events for real-time display.
type StreamHandler interface {
	OnToken(token string)
	OnToolCall(name string, input json.RawMessage)
	OnToolResult(name string, result string)
	OnDone(result *RunResult)
	OnError(err error)
}

// RunStream runs the agent loop and calls handler methods as events occur.
// RunStream processes a user message and streams the assistant's response.
func (r *Runner) RunStream(ctx context.Context, msg memory.Message, handler StreamHandler) {
	ctx, _ = correlation.EnsureRequestID(ctx)
	ctx = correlation.WithSessionID(ctx, r.sessionID)
	ctx = correlation.WithChannel(ctx, r.channelID)
	if p := strings.TrimSpace(r.cfg.RunTracePath); p != "" {
		ctx = runtrace.WithOutputPath(ctx, p)
	}
	ctx, enqueueSpan := obstracing.StartSpan(ctx, "agent.enqueue_message")

	userMessage := msg.Content
	// Append user message to working memory and persistence.
	userMsg := msg
	if userMsg.ID == "" {
		userMsg.ID = uuid.New().String()
	}
	if userMsg.CreatedAt.IsZero() {
		userMsg.CreatedAt = time.Now()
	}
	r.working.Append(userMsg)
	if err := r.memory.Episodic.Save(ctx, userMsg); err != nil {
		r.log.Warn("episodic save failed", zap.Error(err))
	}
	enqueueSpan.End()
	defer func() { r.localIntelScratch = "" }()

	var totalUsage provider.TokenUsage
	var fullResponse strings.Builder
	iterations := 0

	r.prepareLocalIntelScratch(ctx, userMessage)
	r.traceTaskStart(ctx, userMessage)

	// V3: Planning Phase (kept in working memory for the model; not streamed to Slack/chat surfaces).
	plan := ""
	if r.cfg.EnablePlanning {
		r.log.Info("entering planning phase (stream)")
		r.streamThinking(handler, "🤔 **Planning...**\n")
		var err error
		plan, err = r.plan(ctx, userMessage)
		if err != nil {
			r.log.Warn("planning failed", zap.Error(err))
		} else {
			planMsg := memory.Message{
				ID: uuid.New().String(), SessionID: r.sessionID, Role: memory.RoleSystem,
				Content: "[PLAN]\n" + plan, CreatedAt: time.Now(),
			}
			r.working.Append(planMsg)
			r.streamThinking(handler, "\n<details>\n<summary>Execution Plan</summary>\n\n"+plan+"\n\n</details>\n\n")
		}
	}

	// Pre-compaction flush for streaming
	if r.shouldFlush() {
		r.doFlushStream(ctx, handler, &totalUsage)
	}

	for iterations < r.cfg.MaxIterations {
		iterations++
		r.traceLoopIteration = iterations
		fullResponse.Reset()

		req := r.buildRequestV3(ctx, userMessage)
		r.traceModelIteration(ctx, iterations, userMessage, req)

		// V3: Use buildRequestV3 with query context
		modelCtx, modelSpan := obstracing.StartSpan(ctx, "agent.model_call")
		stream, err := r.provider.Stream(modelCtx, req)
		if err != nil {
			modelSpan.End()
			streamErr := fmt.Errorf("agent: stream: %w", err)
			r.emitTraceTaskDone(ctx, iterations, "error", streamErr.Error())
			handler.OnError(streamErr)
			return
		}

		var toolCalls []provider.ContentPart

		for event := range stream {
			switch event.Type {
			case provider.StreamEventText:
				fullResponse.WriteString(event.Text)
				if r.streamInternalAgentUI() {
					handler.OnToken(event.Text)
				}
			case provider.StreamEventToolUse:
				if event.ToolUse != nil {
					toolCalls = append(toolCalls, *event.ToolUse)
				}
			case provider.StreamEventDone:
				if event.Usage != nil {
					totalUsage.PromptTokens += event.Usage.PromptTokens
					totalUsage.CompletionTokens += event.Usage.CompletionTokens
					totalUsage.TotalTokens += event.Usage.TotalTokens
				}
			case provider.StreamEventError:
				if event.Err != nil {
					r.emitTraceTaskDone(ctx, iterations, "error", event.Err.Error())
				} else {
					r.emitTraceTaskDone(ctx, iterations, "error", "stream error (nil)")
				}
				handler.OnError(event.Err)
				return
			}
		}
		modelSpan.End()

		assistantContent := fullResponse.String()
		if len(toolCalls) == 0 {
			if x := extractXMLFunctionCalls(assistantContent); len(x) > 0 {
				toolCalls = x
				assistantContent = stripXMLFunctionBlocks(assistantContent)
			}
		}
		if len(toolCalls) == 0 {
			toolCalls = extractMarkdownBash(assistantContent)
		}
		r.normalizeToolCallNames(toolCalls)
		if strings.TrimSpace(assistantContent) == "" && len(toolCalls) > 0 {
			assistantContent = "[Activating tools...]"
		}

		// Messaging channels: tokens were buffered so pseudo-XML tool markup is not sent mid-flight.
		if !r.streamInternalAgentUI() {
			if strings.TrimSpace(assistantContent) != "" {
				handler.OnToken(assistantContent)
			} else if len(toolCalls) > 0 {
				handler.OnToken("[Activating tools...]")
			}
		}

		assistantMsg := memory.Message{
			ID: uuid.New().String(), SessionID: r.sessionID, Role: memory.RoleAssistant,
			Content: assistantContent, Parts: toolCalls, Model: r.cfg.Model,
			Tokens: totalUsage.CompletionTokens, CreatedAt: time.Now(),
		}
		r.working.Append(assistantMsg)
		_ = r.memory.Episodic.Save(ctx, assistantMsg)

		if len(toolCalls) == 0 {
			r.emitTraceTaskDone(ctx, iterations, "stop", "")
			handler.OnDone(&RunResult{
				SessionID: r.sessionID, Response: fullResponse.String(),
				Iterations: iterations, Usage: totalUsage,
			})
			return
		}

		for _, tc := range toolCalls {
			inputJSON, _ := json.Marshal(tc.ToolInput)
			handler.OnToolCall(tc.ToolName, inputJSON)
			result := r.executeTool(ctx, tc)
			handler.OnToolResult(tc.ToolName, result)

			toolMsg := memory.Message{
				ID: uuid.New().String(), SessionID: r.sessionID, Role: memory.RoleTool,
				Content: result,
				Parts: []provider.ContentPart{
					{
						Type:              provider.ContentTypeToolResult,
						ToolResultID:      tc.ToolUseID,
						ToolResultContent: result,
					},
				},
				CreatedAt: time.Now(),
			}
			r.working.Append(toolMsg)
			_ = r.memory.Episodic.Save(ctx, toolMsg)
		}
	}

	// V3: Reflection Phase
	if r.cfg.EnableReflection {
		r.log.Info("entering reflection phase (stream)")
		r.streamThinking(handler, "\n\n🧐 **Reflecting...**\n")
		critique, success, err := r.reflect(ctx, userMessage, plan)
		if err == nil {
			r.streamThinking(handler, "\n<details>\n<summary>Self-Critique</summary>\n\n"+critique+"\n\n</details>\n")
			if !success && iterations < r.cfg.MaxIterations {
				r.log.Info("self-critique requested retry", zap.String("critique", critique))
			}

			// Store any lessons learned
			if strings.Contains(critique, "<lesson_learned>") && r.cfg.EmbeddingModel != "" {
				lessonText := extractTag(critique, "lesson_learned")
				if lessonText != "" {
					emb, _ := r.provider.Embed(ctx, r.cfg.EmbeddingModel, userMessage)
					_ = r.memory.Semantic.SaveLesson(ctx, memory.Lesson{
						ID: uuid.New().String(), Query: userMessage, Insights: lessonText,
						Success: success, Embedding: emb, CreatedAt: time.Now(),
					})
				}
			}
		}
	}

	maxIterErr := fmt.Errorf("agent: exceeded max iterations (%d)", r.cfg.MaxIterations)
	r.emitTraceTaskDone(ctx, iterations, "max_iterations", maxIterErr.Error())
	handler.OnError(maxIterErr)
}

// HandleChannelMessage is a background message handler for messaging channels.
func (r *Runner) HandleChannelMessage(ctx context.Context, msg channels.Message, replyFn channels.StreamingReplyFunc,
	reactFn channels.ReactionFunc,
	mediaFn channels.MediaReplyFunc,
) {
	metrics.Default().IncMessagesReceived(msg.ChannelID)
	ctx, _ = correlation.EnsureRequestID(ctx)
	ctx = correlation.WithSessionID(ctx, msg.SessionID)
	ctx = correlation.WithChannel(ctx, msg.ChannelID)
	ctx, sendReplySpan := obstracing.StartSpan(ctx, "agent.send_reply")
	defer sendReplySpan.End()

	r.log.Info("inbound message",
		r.logFields(ctx,
			zap.String("text", truncate(msg.Text, 100)),
		)...,
	)

	// Note: In a production system, we would resolve a dedicated Runner instance per SessionID
	// to maintain separate working memories. For now, we share the runner's session logic.

	// Use a dedicated runner instance for this session ID and channel
	sessionRunner := r.WithSession(msg.SessionID)
	sessionRunner.channelID = msg.ChannelID
	sessionRunner.mediaFn = mediaFn

	// Intercept chat commands (first line starting with /); see channels.SlashCommandLine.
	if line := channels.SlashCommandLine(msg); line != "" {
		if handled := sessionRunner.HandleChatCommand(ctx, line, replyFn); handled {
			return
		}
	}

	// First-message bootstrap: if identity/user files are still templates,
	// ask a concise setup question before normal assistant operation.
	if sessionRunner.shouldRunIdentityBootstrap() {
		_ = replyFn("Quick setup so I can be genuinely helpful from day one:\n" +
			"• What should I call you?\n" +
			"• What name should I use for myself?\n" +
			"• Pick my vibe: calm / builder / hacker / witty\n\n" +
			"Reply like: `you=Elross, me=Claw, vibe=builder` (or just say `skip`).")
		bootstrapMsg := memory.Message{
			ID:        uuid.New().String(),
			SessionID: msg.SessionID,
			Role:      memory.RoleAssistant,
			Content:   "Identity bootstrap prompt sent.",
			CreatedAt: time.Now(),
		}
		sessionRunner.working.Append(bootstrapMsg)
		_ = sessionRunner.memory.Episodic.Save(ctx, bootstrapMsg)
		return
	}

	handler := &channelStreamHandler{
		replyFn: replyFn,
		reactFn: reactFn,
		mediaFn: mediaFn,
	}

	// Save user message to memory
	userMsg := memory.Message{
		ID:        msg.ID,
		SessionID: msg.SessionID,
		Role:      memory.RoleUser,
		Content:   msg.Text,
		Parts:     msg.Parts,
		CreatedAt: time.Now(),
	}

	sessionRunner.RunStream(ctx, userMsg, handler)
}

// channelStreamHandler routes agent tokens back to a messaging channel.
type channelStreamHandler struct {
	replyFn channels.StreamingReplyFunc
	reactFn channels.ReactionFunc
	mediaFn channels.MediaReplyFunc
}

func (h *channelStreamHandler) OnToken(token string) {
	_ = h.replyFn(token)
}

func (h *channelStreamHandler) OnToolCall(name string, _ json.RawMessage) {
	if h.reactFn != nil {
		_ = h.reactFn("⏳")
	}
	// Keep chat UX natural: reactions indicate progress; avoid debug-log chatter.
}

func (h *channelStreamHandler) OnToolResult(name string, _ string) {
	// Intentionally no text event; final assistant message should carry the result.
}

func (h *channelStreamHandler) OnDone(_ *RunResult) {
	if h.reactFn != nil {
		_ = h.reactFn("✅")
	}
	_ = h.replyFn("") // signal done
}

func (h *channelStreamHandler) OnError(err error) {
	if h.reactFn != nil {
		_ = h.reactFn("❌")
	}
	_ = h.replyFn(fmt.Sprintf("\n[Error: %v]", err))
}

// normalizeSlashCommand maps legacy slash aliases to canonical /commands.
func normalizeSlashCommand(cmd string) string {
	switch strings.ToLower(cmd) {
	case "/id":
		return "/whoami"
	case "/thinking", "/t":
		return "/think"
	case "/v":
		return "/verbose"
	case "/reason":
		return "/reasoning"
	case "/elev":
		return "/elevated"
	case "/tell":
		return "/steer"
	case "/export":
		return "/export-session"
	case "/plugin":
		return "/plugins"
	case "/persona":
		return "/identity"
	default:
		return cmd
	}
}

// legacySlashCommandStub explains slash commands not implemented on this chat channel.
func legacySlashCommandStub(cmd string) string {
	stubs := map[string]string{
		"/stop":           "Stopping mid-stream isn’t supported on this channel. After the reply finishes, use `/reset` or `/new` to clear context.",
		"/usage":          "Usage/cost toggles: use `/status` for session size. Detailed token accounting is in host logs, not chat yet.",
		"/think":          "Extra reasoning passes: set `agent.planning` / `agent.reflection` in opsintelligence.yaml (both off by default; not a per-chat `/think` toggle).",
		"/verbose":        "Verbose mode: planning/reflection UI is for CLI/web; messaging channels keep replies compact.",
		"/fast":           "Fast mode: not a separate chat toggle — tune model and `agent.planning` in config.",
		"/reasoning":      "Reasoning visibility: not configurable per chat here; use the gateway/CLI for internal UI if enabled.",
		"/elevated":       "Elevated mode: use `security.mode` and tool profiles in opsintelligence.yaml.",
		"/exec":           "Exec defaults: configure tools and security in opsintelligence.yaml / AGENTS.md.",
		"/queue":          "Message queue modes: not exposed in OpsIntelligence chat yet.",
		"/config":         "Config: edit `opsintelligence.yaml` on the host and restart the process.",
		"/mcp":            "MCP: configure under `mcp:` in opsintelligence.yaml.",
		"/plugins":        "Plugins: OpsIntelligence uses `skills/`; see `/skills`.",
		"/debug":          "Debug overrides: use logging on the host (not chat).",
		"/approve":        "Exec approvals: use security / channel settings in yaml, not `/approve` in chat.",
		"/allowlist":      "Allowlists: configure in opsintelligence.yaml / security settings.",
		"/tasks":          "Background tasks: use `cron:` and `opsintelligence cron` on the host; no per-session task list in chat.",
		"/export-session": "Session export: not implemented over chat — use episodic DB / logs on the host.",
		"/bash":           "Shell: ask the agent in natural language; it will use the `bash` tool. Raw `/bash` isn’t wired in chat.",
		"/btw":            "Side questions: send a normal message; use `/new` first if you want minimal prior context.",
		"/session":        "Session idle/max-age: configure channels and sessions in opsintelligence.yaml.",
		"/focus":          "Thread/topic binding: not implemented for this connector; session id is fixed per channel binding.",
		"/unfocus":        "Unfocus: not implemented for this connector.",
		"/agents":         "Thread-bound agents: use `/subagents` for registered specialists + `subagent_*` tools from the agent.",
		"/activation":     "Group activation: configure the Slack channel in opsintelligence.yaml.",
		"/send":           "Send policy: configure the channel in opsintelligence.yaml.",
		"/restart":        "Restart: restart the opsintelligence process on the host (systemd, docker, or terminal).",
		"/tts":            "TTS: configure `voice:` in opsintelligence.yaml and channel voice settings.",
		"/acp":            "ACP: not available in OpsIntelligence.",
		"/skill":          "Skills: use `/skills` to list; invoke by asking the agent or via skill tools in a normal message.",
		"/steer":          "Steering sub-agents: OpsIntelligence sub-agents are one-shot (`subagent_run`). Send a new task in chat or create another run.",
		"/kill":           "Kill sub-agent: runs finish on their own; there is no long-lived kill target in chat.",
	}
	msg, ok := stubs[cmd]
	if !ok {
		return fmt.Sprintf("ℹ️ `%s` is not implemented in OpsIntelligence chat. See `/commands` for what works here.", cmd)
	}
	return "ℹ️ " + msg
}

// HandleChatCommand processes slash commands like /reset, /status.
// Returns true if the message was a command and was handled.
func (r *Runner) HandleChatCommand(ctx context.Context, text string, replyFn channels.StreamingReplyFunc) bool {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return false
	}
	cmd := normalizeSlashCommand(strings.ToLower(parts[0]))

	switch cmd {
	case "/reset":
		r.working.Clear()
		_ = replyFn("✨ Session memory cleared. Starting fresh!")
		return true

	case "/new":
		// Alias for /reset: fresh turn context.
		r.working.Clear()
		_ = replyFn("✨ New session — working memory cleared.")
		return true

	case "/whoami":
		ch := r.channelID
		if ch == "" {
			ch = "(cli/web)"
		}
		_ = replyFn(fmt.Sprintf("🪪 *Whoami*\n• *Session:* `%s`\n• *Channel:* `%s`\n• *Model:* `%s`\n• *State/workspace root:* `%s`\n",
			r.sessionID, ch, r.cfg.Model, r.workspaceDir))
		return true

	case "/identity":
		return r.handleIdentityCommand(parts, replyFn)

	case "/context":
		_ = replyFn("🧩 *How context works (OpsIntelligence)*\n" +
			"• *System prompt:* `SOUL.md`, `IDENTITY.md`, `USER.md`, `AGENTS.md` from the state dir + tool table + rules.\n" +
			"• *Working memory:* recent turns in this session — `/reset` or `/new` clears it.\n" +
			"• *Episodic:* SQLite history — `/sessions`, `/forget`.\n" +
			"• *Tools:* per-request subset from the tool graph (+ skills).\n" +
			"Planning/reflection streams are not copied into Slack-style channels.\n")
		return true

	case "/compact":
		budget := r.working.MaxTokens()
		if budget <= 0 {
			budget = 48000
		}
		target := int(float64(budget) * 0.45)
		dropped := r.working.Compact(target)
		_ = replyFn(fmt.Sprintf("🗜️ Compacted working memory toward ~%d tokens. Dropped %d message(s).", target, len(dropped)))
		return true

	case "/model":
		if len(parts) >= 2 {
			want := strings.Join(parts[1:], " ")
			_ = replyFn(fmt.Sprintf("🔧 *Current model:* `%s`\nTo use `%s`, set `routing.default` or `--model` and *restart* opsintelligence. Chat cannot switch models live yet.", r.cfg.Model, want))
			return true
		}
		_ = replyFn(fmt.Sprintf("🧠 *Current model:* `%s` (provider: `%s`)\n`/models` — full catalog. Change in opsintelligence.yaml + restart.", r.cfg.Model, r.cfg.ProviderName))
		return true

	case "/models":
		if r.modelRegistry == nil {
			_ = replyFn("❌ Model registry not wired in this process. Use: `opsintelligence providers list` on the host.")
			return true
		}
		models := r.modelRegistry.ListModels()
		if len(models) == 0 {
			_ = replyFn("No models registered.")
			return true
		}
		filter := ""
		if len(parts) >= 2 {
			filter = strings.ToLower(strings.Join(parts[1:], " "))
		}
		var b strings.Builder
		if filter != "" {
			b.WriteString(fmt.Sprintf("🧠 *Models* (filter: `%s`)\n", filter))
		} else {
			b.WriteString("🧠 *Models* (`provider/model_id`)\n")
		}
		const maxLines = 120
		shown := 0
		for _, m := range models {
			if filter != "" {
				prov := strings.ToLower(m.Provider)
				id := strings.ToLower(m.ID)
				name := strings.ToLower(m.Name)
				if !strings.Contains(prov, filter) && !strings.Contains(id, filter) && !strings.Contains(name, filter) {
					continue
				}
			}
			if shown >= maxLines {
				b.WriteString("… _truncated_\n")
				break
			}
			line := fmt.Sprintf("• `%s/%s`", m.Provider, m.ID)
			if m.Name != "" && m.Name != m.ID {
				line += fmt.Sprintf(" — %s", m.Name)
			}
			b.WriteString(line + "\n")
			shown++
		}
		if shown == 0 {
			_ = replyFn("No models matched that filter.")
			return true
		}
		_ = replyFn(b.String())
		return true

	case "/tools":
		defs := r.tools.Definitions()
		byName := make(map[string]provider.ToolDef, len(defs))
		names := make([]string, 0, len(defs))
		for _, d := range defs {
			byName[d.Name] = d
			names = append(names, d.Name)
		}
		sort.Strings(names)
		verbose := len(parts) >= 2 && strings.ToLower(parts[1]) == "verbose"
		var tb strings.Builder
		if verbose {
			tb.WriteString("🛠️ *Tools (verbose)*\n")
		} else {
			tb.WriteString("🛠️ *Tools* — `/tools verbose` for one-line descriptions\n")
		}
		const maxTools = 80
		for i, n := range names {
			if i >= maxTools {
				tb.WriteString(fmt.Sprintf("… _and %d more_\n", len(names)-maxTools))
				break
			}
			if verbose {
				desc := strings.TrimSpace(byName[n].Description)
				if len(desc) > 160 {
					desc = desc[:157] + "…"
				}
				if desc == "" {
					desc = "—"
				}
				tb.WriteString(fmt.Sprintf("• `%s` — %s\n", n, desc))
			} else {
				tb.WriteString(fmt.Sprintf("• `%s`\n", n))
			}
		}
		_ = replyFn(tb.String())
		return true

	case "/commands":
		cmds := "📚 *OpsIntelligence chat commands*\n\n*Fully supported*\n"
		cmds += "• `/help` `/commands`\n"
		cmds += "• `/status` — model, provider, session, working-memory size\n"
		cmds += "• `/whoami` (alias `/id`) — session, channel, model, state dir\n"
		cmds += "• `/identity` (alias `/persona`) — show or set assistant identity fields\n"
		cmds += "• `/context` — how prompts + memory + tools are built\n"
		cmds += "• `/compact` — drop oldest working-memory turns (token trim)\n"
		cmds += "• `/model` `[provider/id]` — show current; changing needs yaml + restart\n"
		cmds += "• `/models` `[filter]` — catalog; optional substring filter\n"
		cmds += "• `/tools` `[verbose]` — tool names (+ optional one-line descriptions)\n"
		cmds += "• `/skills` `/subagents` `/sessions` `/forget` `/reset` `/new` `/auto`\n\n"
		cmds += "*Legacy / alternate names* (reply explains OpsIntelligence equivalent)\n"
		cmds += "`/stop` `/usage` `/think` `/t` `/verbose` `/v` `/fast` `/reasoning` `/reason` `/elevated` `/elev` `/exec` `/queue` `/config` `/mcp` `/plugins` `/plugin` `/debug` `/approve` `/allowlist` `/tasks` `/export` `/export-session` `/bash` `/btw` `/session` `/focus` `/unfocus` `/agents` `/activation` `/send` `/restart` `/tts` `/acp` `/skill` `/steer` `/tell` `/kill`\n"
		_ = replyFn(cmds)
		return true

	case "/status":
		status := "🤖 *OpsIntelligence Status*\n"
		status += fmt.Sprintf("• *Model:* `%s`\n", r.cfg.Model)
		status += fmt.Sprintf("• *Provider:* `%s`\n", r.cfg.ProviderName)
		status += fmt.Sprintf("• *Session ID:* `%s`\n", r.sessionID)
		status += fmt.Sprintf("• *Memory Usage:* %d messages\n", len(r.working.Messages()))
		_ = replyFn(status)
		return true

	case "/skills":
		// Find skill_graph_index tool to reuse its logic
		tool, ok := r.tools.Get("skill_graph_index")
		if !ok {
			_ = replyFn("❌ Skill index tool not available.")
			return true
		}

		// Execute the tool (empty input)
		res, err := tool.Execute(ctx, json.RawMessage("{}"))
		if err != nil {
			_ = replyFn(fmt.Sprintf("❌ Failed to list skills: %v", err))
		} else {
			_ = replyFn("🧠 *Skills Report*\n" + res)
		}
		return true

	case "/subagents":
		tool, ok := r.tools.Get("subagent_list")
		if !ok {
			_ = replyFn("❌ Sub-agent tools are not available in this process.")
			return true
		}
		res, err := tool.Execute(ctx, json.RawMessage("{}"))
		if err != nil {
			_ = replyFn(fmt.Sprintf("❌ Failed to list sub-agents: %v", err))
		} else {
			_ = replyFn("🧩 *Sub-agents*\n" + res)
		}
		return true

	case "/sessions":
		ids, err := r.memory.ListSessions(ctx)
		if err != nil {
			_ = replyFn(fmt.Sprintf("❌ Failed to list sessions: %v", err))
			return true
		}
		res := "🗂️ *Active & Historical Sessions*\n"
		for _, id := range ids {
			marker := "•"
			if id == r.sessionID {
				marker = "⭐️"
			}
			res += fmt.Sprintf("%s `%s`\n", marker, id)
		}
		_ = replyFn(res)
		return true

	case "/forget":
		target := r.sessionID
		if len(parts) > 1 {
			target = parts[1]
		}
		err := r.memory.DeleteSession(ctx, target)
		if err != nil {
			_ = replyFn(fmt.Sprintf("❌ Failed to forget session `%s`: %v", target, err))
		} else {
			_ = replyFn(fmt.Sprintf("🛡️ Session `%s` has been permanently forgotten.", target))
		}
		return true

	case "/auto":
		goal := strings.Join(parts[1:], " ")
		if goal == "" {
			_ = replyFn("❌ Please provide a goal. Usage: /auto <goal>")
			return true
		}
		_ = replyFn(fmt.Sprintf("🚀 Starting autonomous agent in background. Goal: %s", goal))
		go func() {
			// run in background
			res, err := r.RunAutonomous(context.Background(), goal)
			if err != nil {
				_ = replyFn(fmt.Sprintf("❌ Autonomous task failed: %v", err))
			} else {
				_ = replyFn(fmt.Sprintf("✅ Autonomous task finished: %s", res.Response))
			}
		}()
		return true

	case "/help":
		help := "📋 *Commands* — `/commands` for full list\n"
		help += "• *Core:* `/status` `/whoami` `/context` `/compact`\n"
		help += "• *Persona:* `/identity show` `/identity set name=... vibe=... emoji=... creature=...`\n"
		help += "• *Models/tools:* `/model` `/models` `/tools` `/tools verbose` `/skills` `/subagents`\n"
		help += "• *Session:* `/reset` `/new` `/sessions` `/forget` `/auto`\n"
		help += "• *Legacy names:* `/stop` `/usage` `/think` … → stub with pointers\n"
		help += "• `/help` `/commands`\n"
		_ = replyFn(help)
		return true

	case "/stop", "/usage", "/think", "/verbose", "/fast", "/reasoning", "/elevated", "/exec", "/queue", "/config", "/mcp", "/plugins", "/debug", "/approve", "/allowlist", "/tasks", "/export-session", "/bash", "/btw", "/session", "/focus", "/unfocus", "/agents", "/activation", "/send", "/restart", "/tts", "/acp", "/skill", "/steer", "/kill":
		_ = replyFn(legacySlashCommandStub(cmd))
		return true
	}

	return false
}

func (r *Runner) shouldRunIdentityBootstrap() bool {
	// Messaging channels only; CLI/web has its own onboarding flow.
	if r.channelID == "" {
		return false
	}
	// Do it once per session.
	if len(r.working.Messages()) > 0 {
		return false
	}
	idPath := filepath.Join(r.workspaceDir, "IDENTITY.md")
	userPath := filepath.Join(r.workspaceDir, "USER.md")
	idData, idErr := os.ReadFile(idPath)
	userData, userErr := os.ReadFile(userPath)
	if idErr != nil && userErr != nil {
		return false
	}
	idTemplate := idErr == nil && looksLikeTemplate(string(idData))
	userTemplate := userErr == nil && looksLikeTemplate(string(userData))
	return idTemplate || userTemplate
}

func (r *Runner) handleIdentityCommand(parts []string, replyFn channels.StreamingReplyFunc) bool {
	usage := "Usage:\n" +
		"• `/identity show`\n" +
		"• `/identity set name=<...> vibe=<...> emoji=<...> creature=<...>`\n" +
		"Example: `/identity set name=Claw vibe=builder emoji=🛠️ creature=fox`"

	if len(parts) < 2 || strings.ToLower(parts[1]) == "show" {
		idPath := filepath.Join(r.workspaceDir, "IDENTITY.md")
		data, err := os.ReadFile(idPath)
		if err != nil {
			_ = replyFn("❌ Could not read IDENTITY.md.\n" + usage)
			return true
		}
		_ = replyFn("🪪 *Identity* (`IDENTITY.md`)\n" + summarizeIdentity(string(data)))
		return true
	}

	if strings.ToLower(parts[1]) != "set" {
		_ = replyFn("❌ Unknown `/identity` subcommand.\n" + usage)
		return true
	}

	if len(parts) < 3 {
		_ = replyFn("❌ Missing fields.\n" + usage)
		return true
	}

	updates := map[string]string{}
	for _, token := range parts[2:] {
		kv := strings.SplitN(token, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[0]))
		v := strings.TrimSpace(kv[1])
		if v == "" {
			continue
		}
		updates[k] = v
	}

	if len(updates) == 0 {
		_ = replyFn("❌ No valid `key=value` fields found.\n" + usage)
		return true
	}

	idPath := filepath.Join(r.workspaceDir, "IDENTITY.md")
	data, err := os.ReadFile(idPath)
	if err != nil {
		_ = replyFn("❌ Could not read IDENTITY.md.")
		return true
	}
	updated := string(data)
	fieldMap := map[string]string{
		"name":     "Name",
		"creature": "Creature",
		"vibe":     "Vibe",
		"emoji":    "Emoji",
		"avatar":   "Avatar",
	}
	changed := 0
	for k, field := range fieldMap {
		if v, ok := updates[k]; ok {
			next, did := setIdentityField(updated, field, v)
			updated = next
			if did {
				changed++
			}
		}
	}

	if changed == 0 {
		_ = replyFn("ℹ️ No supported fields changed.\n" + usage)
		return true
	}
	if err := os.WriteFile(idPath, []byte(updated), 0o644); err != nil {
		_ = replyFn(fmt.Sprintf("❌ Failed to update IDENTITY.md: %v", err))
		return true
	}
	_ = replyFn("✅ Updated `IDENTITY.md`.\n" + summarizeIdentity(updated))
	return true
}

func summarizeIdentity(content string) string {
	fields := []string{"Name", "Creature", "Vibe", "Emoji", "Avatar"}
	var out []string
	for _, f := range fields {
		v := getIdentityField(content, f)
		if v == "" {
			v = "—"
		}
		out = append(out, fmt.Sprintf("• *%s:* %s", f, v))
	}
	return strings.Join(out, "\n")
}

func getIdentityField(content, field string) string {
	re := regexp.MustCompile(`(?m)^- \*\*` + regexp.QuoteMeta(field) + `:\*\*\s*(.*)$`)
	m := re.FindStringSubmatch(content)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func setIdentityField(content, field, value string) (string, bool) {
	re := regexp.MustCompile(`(?m)^- \*\*` + regexp.QuoteMeta(field) + `:\*\*.*$`)
	line := fmt.Sprintf("- **%s:** %s", field, value)
	if re.MatchString(content) {
		return re.ReplaceAllString(content, line), true
	}
	// Append if the field is missing.
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + line + "\n", true
}

func (r *Runner) shouldFlush() bool {
	budget := r.working.MaxTokens()
	current := r.working.TotalTokens()
	// Flush if we are at 80% capacity
	return current > int(float64(budget)*0.8)
}

func (r *Runner) doFlush(ctx context.Context, usage *provider.TokenUsage) {
	date := time.Now().Format("2006-01-02")
	prompt := fmt.Sprintf("MEMORY NEAR CAPACITY. Store important info to MEMORY.md or memory/%s.md now if needed. Reply with [SILENT] if nothing to store.", date)

	// Inject a system-like user message for the flush turn
	flushMsg := memory.Message{
		ID: uuid.New().String(), SessionID: r.sessionID, Role: memory.RoleUser,
		Content: prompt, CreatedAt: time.Now(),
	}
	r.working.Append(flushMsg)
	r.log.Info("memory near capacity, triggering flush turn")

	req := r.buildRequest()
	stream, err := r.provider.Stream(ctx, req)
	if err != nil {
		r.log.Warn("memory flush turn failed", zap.Error(err))
		return
	}

	resp, err := provider.CollectStream(ctx, stream)
	if err != nil {
		r.log.Warn("memory flush turn collect failed", zap.Error(err))
		return
	}

	usage.PromptTokens += resp.Usage.PromptTokens
	usage.CompletionTokens += resp.Usage.CompletionTokens
	usage.TotalTokens += resp.Usage.TotalTokens

	assistantContent := resp.Text()
	if strings.TrimSpace(assistantContent) == "" && len(resp.ToolCalls()) > 0 {
		assistantContent = "[Activating tools...]"
	}

	assistantMsg := memory.Message{
		ID: uuid.New().String(), SessionID: r.sessionID, Role: memory.RoleAssistant,
		Content: assistantContent, Parts: resp.ToolCalls(), Model: r.cfg.Model, Tokens: resp.Usage.CompletionTokens, CreatedAt: time.Now(),
	}
	r.working.Append(assistantMsg)

	// Execute any tools (like write_file) requested during flush.
	for _, tc := range resp.ToolCalls() {
		result := r.executeTool(ctx, tc)
		toolMsg := memory.Message{
			ID: uuid.New().String(), SessionID: r.sessionID, Role: memory.RoleTool,
			Content: result,
			Parts: []provider.ContentPart{
				{
					Type:              provider.ContentTypeToolResult,
					ToolResultID:      tc.ToolUseID,
					ToolResultContent: result,
				},
			},
			CreatedAt: time.Now(),
		}
		r.working.Append(toolMsg)
	}
}

func (r *Runner) doFlushStream(ctx context.Context, handler StreamHandler, usage *provider.TokenUsage) {
	date := time.Now().Format("2006-01-02")
	prompt := fmt.Sprintf("MEMORY NEAR CAPACITY. Store important info to MEMORY.md or memory/%s.md now if needed. Reply with [SILENT] if nothing to store.", date)

	flushMsg := memory.Message{
		ID: uuid.New().String(), SessionID: r.sessionID, Role: memory.RoleUser,
		Content: prompt, CreatedAt: time.Now(),
	}
	r.working.Append(flushMsg)
	r.streamThinking(handler, "\n[Maintenance: Compacting session memory...]\n")

	stream, err := r.provider.Stream(ctx, r.buildRequest())
	if err != nil {
		handler.OnError(fmt.Errorf("agent: flush stream: %w", err))
		return
	}

	var fullResponse strings.Builder
	var toolCalls []provider.ContentPart

	for event := range stream {
		switch event.Type {
		case provider.StreamEventText:
			handler.OnToken(event.Text)
			fullResponse.WriteString(event.Text)
		case provider.StreamEventToolUse:
			if event.ToolUse != nil {
				toolCalls = append(toolCalls, *event.ToolUse)
			}
		case provider.StreamEventDone:
			if event.Usage != nil {
				usage.PromptTokens += event.Usage.PromptTokens
				usage.CompletionTokens += event.Usage.CompletionTokens
				usage.TotalTokens += event.Usage.TotalTokens
			}
		case provider.StreamEventError:
			handler.OnError(event.Err)
			return
		}
	}

	assistantContent := fullResponse.String()
	if strings.TrimSpace(assistantContent) == "" && len(toolCalls) > 0 {
		assistantContent = "[Activating tools...]"
	}

	assistantMsg := memory.Message{
		ID: uuid.New().String(), SessionID: r.sessionID, Role: memory.RoleAssistant,
		Content: assistantContent, Parts: toolCalls, Model: r.cfg.Model,
		Tokens: usage.CompletionTokens, CreatedAt: time.Now(),
	}
	r.working.Append(assistantMsg)

	for _, tc := range toolCalls {
		inputJSON, _ := json.Marshal(tc.ToolInput)
		handler.OnToolCall(tc.ToolName, inputJSON)
		result := r.executeTool(ctx, tc)
		handler.OnToolResult(tc.ToolName, result)

		toolMsg := memory.Message{
			ID: uuid.New().String(), SessionID: r.sessionID, Role: memory.RoleTool,
			Content: result,
			Parts: []provider.ContentPart{
				{
					Type:              provider.ContentTypeToolResult,
					ToolResultID:      tc.ToolUseID,
					ToolResultContent: result,
				},
			},
			CreatedAt: time.Now(),
		}
		r.working.Append(toolMsg)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
