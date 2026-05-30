package tuibridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/subagents"
)

// ── Public types passed in by callers (mirror cmd/opsintelligence/tui types) ──

// DashboardStatus is the static status payload (PID, version, channels, …).
// Live values (CPU/RSS/etime) are derived from `ps` on each tick.
type DashboardStatus struct {
	PID           int
	Version       string
	SkillSummary  string
	Channels      []string
	PlanoEnabled  bool
	PlanoEndpoint string
	MCPEnabled    bool
	MCPTransport  string
	GatewayBase   string
	GatewayBind   string
	RunTraceFile  string
	RunTraceMode  string
}

// DashboardLimits mirrors the numeric config shown on the Limits tab.
type DashboardLimits struct {
	MaxIterations         int
	WorkingTokenBudget    int
	MemPalaceSearchLimit  int
	MaxWebSocketClients   int
	SubagentMaxConcurrent int
	SubagentRetainLimit   int
	LocalIntelMaxTokens   int
	SmartRoutingMaxTokens int
}

// DashboardInfo is the input snapshot used to drive the dashboard.
// Identical shape to the legacy tui.DashboardInfo so callers can swap one for the other.
type DashboardInfo struct {
	Status DashboardStatus

	ConfigPath        string
	StateDir          string
	CWD               string
	RoutingModel      string
	GatewayHostPort   string
	GatewayPublicBase string
	Enterprise        bool
	Planning          string
	Reflection        string
	MemPalaceEnabled  bool
	LocalIntelEnabled bool
	MCPClientCount    int

	Limits DashboardLimits

	Tasks             *subagents.TaskManager
	RunTracePath      string
	SubagentTracePath string
	LogsDir           string
	DatastoreKind     string
}

// SessionUsage matches the REPL footer accumulator. Optional.
type SessionUsage struct {
	Turns            int
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	CacheWriteTokens int
	TotalTokens      int
}

// DashboardOptions configures RunDashboard.
type DashboardOptions struct {
	Info         DashboardInfo
	ContextLabel string
	SessionUsage *SessionUsage
	Overlay      bool
	LogDir       string

	// EditConfig, when non-nil, lets the Rust TUI patch opsintelligence.yaml
	// directly from the dashboard via `e` on an editable row. The callback
	// receives the dotted yaml_path (e.g. "agent.max_iterations") and the new
	// value as a string. Return `(humanMsg, nil)` on success and `(_, err)`
	// to surface a one-line error to the user. The host typically wires this
	// to mergeOnboardYAML + os.WriteFile.
	EditConfig func(yamlPath, value string) (string, error)
}

// RunDashboard launches the embedded Rust TUI in dashboard mode and pumps a
// fresh snapshot to it every second. Blocks until the user quits or the
// subprocess exits.
func RunDashboard(ctx context.Context, opts DashboardOptions) error {
	quit := make(chan struct{})
	var bridge *Bridge
	sendBack := func(method string, params any) {
		if bridge != nil {
			_ = bridge.Send(method, params)
		}
	}

	handler := func(msg Message) {
		switch msg.Method {
		case "view.exit", "view.dismiss":
			select {
			case <-quit:
			default:
				close(quit)
			}
		case "dashboard.edit":
			// {yaml_path: "...", value: "..."} — patch opsintelligence.yaml
			// via the same merge path the wizard uses, then echo a result.
			var p struct {
				YamlPath string `json:"yaml_path"`
				Value    string `json:"value"`
			}
			if err := json.Unmarshal(msg.Params, &p); err != nil {
				sendBack("dashboard.edit_result", map[string]any{
					"ok":    false,
					"error": fmt.Sprintf("parse: %v", err),
				})
				return
			}
			if opts.EditConfig == nil {
				sendBack("dashboard.edit_result", map[string]any{
					"ok":        false,
					"yaml_path": p.YamlPath,
					"error":     "edit handler not configured by host",
				})
				return
			}
			msgStr, err := opts.EditConfig(p.YamlPath, p.Value)
			res := map[string]any{
				"ok":        err == nil,
				"yaml_path": p.YamlPath,
				"message":   msgStr,
			}
			if err != nil {
				res["error"] = err.Error()
			}
			sendBack("dashboard.edit_result", res)
		}
	}

	b, err := Spawn(ctx, Options{Handler: handler, LogDir: opts.LogDir})
	if err != nil {
		return err
	}
	bridge = b
	defer func() { _ = b.Close(2 * time.Second) }()

	if err := b.Send("view.push", map[string]any{
		"view": "dashboard",
		"dashboard": map[string]any{
			"context_label": opts.ContextLabel,
			"overlay":       opts.Overlay,
		},
	}); err != nil {
		return err
	}

	// Send first snapshot immediately, then on a 1s tick.
	cache := newDashboardCache(opts.Info)
	if err := b.Send("dashboard.snapshot", cache.snapshot(opts)); err != nil {
		return err
	}

	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-quit:
			return nil
		case <-b.Done():
			return b.CloseErr()
		case <-ctx.Done():
			return ctx.Err()
		case <-tk.C:
			_ = b.Send("dashboard.snapshot", cache.snapshot(opts))
		}
	}
}

// ── Snapshot builder ──────────────────────────────────────────────────────

type dashboardCache struct {
	info        DashboardInfo
	logOffsets  map[string]int64
	logAccum    []logEntry
	maxLogLines int
}

func newDashboardCache(info DashboardInfo) *dashboardCache {
	return &dashboardCache{
		info:        info,
		logOffsets:  make(map[string]int64),
		maxLogLines: 300,
	}
}

// kvPair is one row on the Config / Limits / Usage tabs. YamlPath, when set,
// flags the row as editable in the Rust TUI: pressing `e` opens an inline
// editor and the resulting value is patched into opsintelligence.yaml via
// the dashboard.edit handler.
type kvPair struct {
	K        string   `json:"k"`
	V        string   `json:"v"`
	YamlPath string   `json:"yaml_path,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	Hint     string   `json:"hint,omitempty"`
}

// kv constructs a read-only row (no yaml_path, no edit).
func kv(k, v string) kvPair { return kvPair{K: k, V: v} }

// kvEdit constructs an editable row backed by a yaml path. `hint` is a
// short one-line description shown under the editor (e.g. "integer ≥ 1").
func kvEdit(k, v, yamlPath, hint string) kvPair {
	return kvPair{K: k, V: v, YamlPath: yamlPath, Hint: hint}
}

// kvSelect is like kvEdit but presents `choices` as a vertical select list
// instead of free-form text input. Use for booleans and enums.
func kvSelect(k, v, yamlPath string, choices []string) kvPair {
	return kvPair{K: k, V: v, YamlPath: yamlPath, Choices: choices}
}

type snapStatus struct {
	Alive         bool     `json:"alive"`
	PID           int      `json:"pid"`
	Etime         string   `json:"etime"`
	CPUPercent    float64  `json:"cpu_percent"`
	RSSMB         float64  `json:"rss_mb"`
	Version       string   `json:"version"`
	SkillSummary  string   `json:"skill_summary"`
	Channels      []string `json:"channels"`
	Plano         toggle   `json:"plano"`
	MCP           toggle   `json:"mcp"`
	GatewayBase   string   `json:"gateway_base"`
	GatewayBind   string   `json:"gateway_bind"`
	RunTraceFile  string   `json:"run_trace_file"`
}

type toggle struct {
	Enabled bool   `json:"enabled"`
	Detail  string `json:"detail"`
}

type agentInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Elapsed     string `json:"elapsed"`
	LastPhase   string `json:"last_phase"`
	LastMessage string `json:"last_message"`
	Error       string `json:"error"`
}

type logSnap struct {
	Time   string `json:"time"`
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Error  bool   `json:"error"`
}

type snapshot struct {
	Status         snapStatus  `json:"status"`
	Config         []kvPair    `json:"config"`
	Limits         []kvPair    `json:"limits"`
	Usage          []kvPair    `json:"usage"`
	UsageEmptyHint string      `json:"usage_empty_hint"`
	Agents         []agentInfo `json:"agents"`
	Logs           []logSnap   `json:"logs"`
	LogSourcePath  string      `json:"log_source_path"`
}

func (c *dashboardCache) snapshot(opts DashboardOptions) snapshot {
	ps := fetchPS(c.info.Status.PID)
	if path := os.Getenv("OPSINTEL_TUI_DEBUG"); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "[dash-snap] info.PID=%d info.Version=%q ps.alive=%v ps.cpu=%s\n",
				c.info.Status.PID, c.info.Status.Version, ps.alive, ps.cpu)
			_ = f.Close()
		}
	}
	return snapshot{
		Status:         c.buildStatus(ps),
		Config:         c.buildConfig(),
		Limits:         c.buildLimits(),
		Usage:          c.buildUsage(opts.SessionUsage, ps),
		UsageEmptyHint: "Session token usage appears after you send messages in the agent REPL.",
		Agents:         c.buildAgents(),
		Logs:           c.refreshAndBuildLogs(),
		LogSourcePath:  c.info.RunTracePath,
	}
}

func (c *dashboardCache) buildStatus(ps psResult) snapStatus {
	cpu := 0.0
	fmt.Sscanf(ps.cpu, "%f", &cpu)
	return snapStatus{
		Alive:         ps.alive,
		PID:           c.info.Status.PID,
		Etime:         ps.etime,
		CPUPercent:    cpu,
		RSSMB:         float64(ps.rssKB) / 1024.0,
		Version:       c.info.Status.Version,
		SkillSummary:  c.info.Status.SkillSummary,
		Channels:      c.info.Status.Channels,
		Plano:         toggle{Enabled: c.info.Status.PlanoEnabled, Detail: c.info.Status.PlanoEndpoint},
		MCP:           toggle{Enabled: c.info.Status.MCPEnabled, Detail: c.info.Status.MCPTransport},
		GatewayBase:   c.info.Status.GatewayBase,
		GatewayBind:   c.info.Status.GatewayBind,
		RunTraceFile:  c.info.Status.RunTraceFile,
	}
}

func (c *dashboardCache) buildConfig() []kvPair {
	i := c.info
	return []kvPair{
		kv("config file", nz(i.ConfigPath)),
		kv("state_dir", nz(i.StateDir)),
		kv("cwd", nz(i.CWD)),
		kvEdit("routing.default", nz(i.RoutingModel), "routing.default",
			"<provider>/<model> — e.g. anthropic/claude-sonnet-4-5"),
		kv("gateway.listen", nz(i.GatewayHostPort)),       // host:port; restart-required
		kv("gateway.public", nz(i.GatewayPublicBase)),     // derived
		kvSelect("enterprise", boolStr(i.Enterprise), "enterprise.enabled",
			[]string{"true", "false"}),
		kvSelect("planning", nz(i.Planning), "agent.planning.mode",
			[]string{"off", "lightweight", "auto", "deep"}),
		kvSelect("reflection", nz(i.Reflection), "agent.reflection",
			[]string{"true", "false"}),
		kvSelect("mempalace", boolStr(i.MemPalaceEnabled), "memory.mempalace.enabled",
			[]string{"true", "false"}),
		kvSelect("local_intel", boolStr(i.LocalIntelEnabled), "agent.local_intel.enabled",
			[]string{"true", "false"}),
		kv("mcp.clients", strconv.Itoa(i.MCPClientCount)),
	}
}

func (c *dashboardCache) buildLimits() []kvPair {
	L := c.info.Limits
	ds := nz(c.info.DatastoreKind)
	if ds == "—" {
		ds = "sqlite"
	}
	maxSessions := "~50–100  (SQLite WAL, single-writer)"
	if ds == "postgres" {
		maxSessions = "~500+  (tune max_open_conns)"
	}
	return []kvPair{
		kvEdit("max_iterations", strconv.Itoa(L.MaxIterations), "agent.max_iterations",
			"integer ≥ 1 — agent's per-turn step budget"),
		kvEdit("working_token_budget", strconv.Itoa(L.WorkingTokenBudget), "memory.working_token_budget",
			"integer ≥ 1 — running-conversation token budget"),
		kvEdit("mempalace.search_limit", strconv.Itoa(L.MemPalaceSearchLimit), "memory.mempalace.search_limit",
			"integer ≥ 1 — top-K memory hits per turn"),
		kvEdit("gateway.max_ws_clients", strconv.Itoa(L.MaxWebSocketClients), "gateway.max_websocket_clients",
			"integer ≥ 1 — concurrent dashboard WS connections"),
		kvEdit("subagent.max_concurrent", strconv.Itoa(L.SubagentMaxConcurrent), "agent.subagent_tasks.max_concurrent",
			"integer ≥ 1 — parallel sub-agents per turn"),
		kvEdit("subagent.retain_limit", strconv.Itoa(L.SubagentRetainLimit), "agent.subagent_tasks.retain_limit",
			"integer ≥ 1 — finished sub-tasks kept for replay"),
		kvEdit("local_intel.max_tokens", strconv.Itoa(L.LocalIntelMaxTokens), "agent.local_intel.max_tokens",
			"integer ≥ 0"),
		kvEdit("local_intel.smart_route_max", strconv.Itoa(L.SmartRoutingMaxTokens), "agent.local_intel.smart_routing_max_tokens",
			"integer ≥ 0"),
		kv("session_store", "in-process RAM"),
		kv("datastore", ds),
		kv("horizontal_scaling", "✗  single-instance"),
		kv("recommended_max_sessions", maxSessions),
	}
}

func (c *dashboardCache) buildUsage(s *SessionUsage, ps psResult) []kvPair {
	if s != nil && s.Turns > 0 {
		return []kvPair{
			kv("turns_completed", strconv.Itoa(s.Turns)),
			kv("prompt_tokens", fmtNumInt(s.PromptTokens)),
			kv("completion_tokens", fmtNumInt(s.CompletionTokens)),
			kv("cache_read", fmtNumInt(s.CacheReadTokens)),
			kv("cache_write", fmtNumInt(s.CacheWriteTokens)),
			kv("total_tokens", fmtNumInt(s.TotalTokens)),
		}
	}
	if c.info.Status.PID > 0 && ps.alive {
		return []kvPair{
			kv("process_rss", fmt.Sprintf("%.1f MB", float64(ps.rssKB)/1024.0)),
			kv("pid", strconv.Itoa(c.info.Status.PID)),
		}
	}
	return nil
}

func (c *dashboardCache) buildAgents() []agentInfo {
	if c.info.Tasks == nil {
		return nil
	}
	tasks := c.info.Tasks.List()
	out := make([]agentInfo, 0, len(tasks))
	for _, tk := range tasks {
		ai := agentInfo{
			ID:      tk.ID,
			Name:    tk.SubAgentNm,
			Status:  string(tk.Status),
			Elapsed: tk.Elapsed().Round(time.Second).String(),
			Error:   tk.Error,
		}
		ev := tk.LastEvent()
		if ev.Message != "" {
			ai.LastPhase = ev.Phase
			if ai.LastPhase == "" {
				ai.LastPhase = string(ev.Kind)
			}
			ai.LastMessage = truncRunes(ev.Message, 80)
		}
		out = append(out, ai)
	}
	return out
}

// ── Log scanning (ported from cmd/opsintelligence/tui/dashboard.go) ──────

type logEntry struct {
	T            string   `json:"t"`
	Kind         string   `json:"kind"`
	RunnerRole   string   `json:"runner_role"`
	Tool         string   `json:"tool"`
	Result       string   `json:"result"`
	Iteration    int      `json:"iteration"`
	QueryPreview string   `json:"query_preview"`
	Error        string   `json:"error"`
	Finish       string   `json:"finish_reason"`
	ToolsOffered []string `json:"tools_offered"`
	Source       string   `json:"-"`
}

type traceFileInfo struct {
	path   string
	source string
}

func (c *dashboardCache) refreshAndBuildLogs() []logSnap {
	sources := collectTracePaths(c.info.LogsDir, c.info.RunTracePath, c.info.SubagentTracePath)
	added := 0
	for _, tf := range sources {
		off := c.logOffsets[tf.path]
		entries := readTraceFile(tf.path, &off, tf.source)
		c.logOffsets[tf.path] = off
		if len(entries) > 0 {
			c.logAccum = append(c.logAccum, entries...)
			added += len(entries)
		}
	}
	if added > 0 {
		sort.Slice(c.logAccum, func(i, j int) bool { return c.logAccum[i].T < c.logAccum[j].T })
		if len(c.logAccum) > c.maxLogLines {
			c.logAccum = append([]logEntry(nil), c.logAccum[len(c.logAccum)-c.maxLogLines:]...)
		}
	}

	out := make([]logSnap, 0, len(c.logAccum))
	for _, e := range c.logAccum {
		var detail string
		switch e.Kind {
		case "task_start":
			detail = e.QueryPreview
		case "model_iteration":
			detail = fmt.Sprintf("iter=%d  tools=%d", e.Iteration, len(e.ToolsOffered))
		case "tool_call", "tool_result", "tool_done":
			detail = e.Tool
			if e.Result != "" {
				detail += "  → " + truncRunes(e.Result, 50)
			}
		case "task_done":
			detail = "finish=" + e.Finish
			if e.Error != "" {
				detail += "  err=" + e.Error
			}
		default:
			detail = e.Kind
		}
		ts := e.T
		if len(ts) >= 19 {
			ts = ts[11:19]
		}
		source := e.Source
		if source == "" {
			source = e.RunnerRole
		}
		out = append(out, logSnap{
			Time:   ts,
			Source: source,
			Kind:   e.Kind,
			Detail: detail,
			Error:  e.Error != "",
		})
	}
	return out
}

func collectTracePaths(logsDir, fixedMaster, fixedSub string) []traceFileInfo {
	if logsDir == "" {
		var out []traceFileInfo
		if fixedMaster != "" {
			out = append(out, traceFileInfo{fixedMaster, "master"})
		}
		if fixedSub != "" && fixedSub != fixedMaster {
			out = append(out, traceFileInfo{fixedSub, "subagents"})
		}
		return out
	}
	skip := map[string]bool{"audit": true, "pipeline": true}
	var out []traceFileInfo
	_ = filepath.WalkDir(logsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && skip[d.Name()] {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "runtrace.ndjson" {
			rel, _ := filepath.Rel(logsDir, path)
			out = append(out, traceFileInfo{path, labelFromRelPath(rel)})
		}
		return nil
	})
	return out
}

func labelFromRelPath(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch {
	case len(parts) >= 1 && parts[0] == "agent":
		return "master"
	case len(parts) >= 2 && parts[0] == "subagents":
		if parts[1] == "runtrace.ndjson" {
			return "subagents"
		}
		id := parts[1]
		if len(id) > 12 {
			id = id[:12]
		}
		return "sub:" + id
	case len(parts) >= 2 && parts[0] == "repointel":
		name := parts[1]
		if len(name) > 20 {
			name = name[:20]
		}
		return "repointel:" + name
	default:
		if len(parts) >= 2 {
			return parts[0] + ":" + parts[1]
		}
		return rel
	}
}

func readTraceFile(path string, offset *int64, source string) []logEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, statErr := f.Stat()
	if statErr != nil || fi.Size() <= *offset {
		return nil
	}
	if _, seekErr := f.Seek(*offset, io.SeekStart); seekErr != nil {
		return nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 256*1024), 1024*1024)
	var entries []logEntry
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e logEntry
		if json.Unmarshal(line, &e) == nil {
			e.Source = source
			entries = append(entries, e)
		}
	}
	if pos, err := f.Seek(0, io.SeekCurrent); err == nil {
		*offset = pos
	}
	return entries
}

// ── ps helper (ported from tui/status.go::fetchPS) ───────────────────────

type psResult struct {
	cpu   string
	rssKB int64
	vsz   string
	etime string
	alive bool
}

func fetchPS(pid int) psResult {
	r := psResult{cpu: "0.0", rssKB: 0, vsz: "0", etime: "--:--", alive: false}
	if pid <= 0 {
		return r
	}
	// Ground truth first: does the PID exist? `kill -0` (signal 0) returns
	// no-op success if the process is alive, error otherwise. This catches the
	// case where `ps -o` parsing fails on a platform whose column headers
	// differ from what we expect — the pill should still say RUNNING.
	if p, err := os.FindProcess(pid); err == nil {
		if err := p.Signal(syscall.Signal(0)); err == nil {
			r.alive = true
		}
	}

	// `--no-headers` is procps-ng only; macOS / BSD `ps` rejects it. Try the
	// portable form first; if both invocations fail or produce no data line,
	// keep the defaults but preserve `alive` from the signal-0 probe above.
	out, err := exec.Command("ps", "-p", fmt.Sprint(pid), "-o", "%cpu,rss,vsz,etime").Output()
	if err != nil {
		out, _ = exec.Command("ps", "-p", fmt.Sprint(pid), "-o", "%cpu,rss,vsz,etime", "--no-headers").Output()
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// Skip header row on platforms that print one (macOS/BSD always do).
		if strings.EqualFold(fields[0], "%CPU") || strings.EqualFold(fields[0], "CPU") {
			continue
		}
		r.cpu = fields[0]
		if kb, e := strconv.ParseInt(fields[1], 10, 64); e == nil {
			r.rssKB = kb
		}
		r.vsz = fields[2]
		r.etime = fields[3]
		r.alive = true
		break
	}
	return r
}

// ── small string helpers ─────────────────────────────────────────────────

func nz(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "—"
	}
	return s
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func fmtNumInt(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
