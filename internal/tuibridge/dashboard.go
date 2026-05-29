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
}

// RunDashboard launches the embedded Rust TUI in dashboard mode and pumps a
// fresh snapshot to it every second. Blocks until the user quits or the
// subprocess exits.
func RunDashboard(ctx context.Context, opts DashboardOptions) error {
	quit := make(chan struct{})
	handler := func(msg Message) {
		switch msg.Method {
		case "view.exit", "view.dismiss":
			select {
			case <-quit:
			default:
				close(quit)
			}
		}
	}

	b, err := Spawn(ctx, Options{Handler: handler, LogDir: opts.LogDir})
	if err != nil {
		return err
	}
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
			return b.Err()
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

type kvPair struct {
	K string `json:"k"`
	V string `json:"v"`
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
		{"config file", nz(i.ConfigPath)},
		{"state_dir", nz(i.StateDir)},
		{"cwd", nz(i.CWD)},
		{"routing.default", nz(i.RoutingModel)},
		{"gateway.listen", nz(i.GatewayHostPort)},
		{"gateway.public", nz(i.GatewayPublicBase)},
		{"enterprise", boolStr(i.Enterprise)},
		{"planning", nz(i.Planning)},
		{"reflection", nz(i.Reflection)},
		{"mempalace", boolStr(i.MemPalaceEnabled)},
		{"local_intel", boolStr(i.LocalIntelEnabled)},
		{"mcp.clients", strconv.Itoa(i.MCPClientCount)},
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
		{"max_iterations", strconv.Itoa(L.MaxIterations)},
		{"working_token_budget", strconv.Itoa(L.WorkingTokenBudget)},
		{"mempalace.search_limit", strconv.Itoa(L.MemPalaceSearchLimit)},
		{"gateway.max_ws_clients", strconv.Itoa(L.MaxWebSocketClients)},
		{"subagent.max_concurrent", strconv.Itoa(L.SubagentMaxConcurrent)},
		{"subagent.retain_limit", strconv.Itoa(L.SubagentRetainLimit)},
		{"local_intel.max_tokens", strconv.Itoa(L.LocalIntelMaxTokens)},
		{"local_intel.smart_route_max", strconv.Itoa(L.SmartRoutingMaxTokens)},
		{"session_store", "in-process RAM"},
		{"datastore", ds},
		{"horizontal_scaling", "✗  single-instance"},
		{"recommended_max_sessions", maxSessions},
	}
}

func (c *dashboardCache) buildUsage(s *SessionUsage, ps psResult) []kvPair {
	if s != nil && s.Turns > 0 {
		return []kvPair{
			{"turns_completed", strconv.Itoa(s.Turns)},
			{"prompt_tokens", fmtNumInt(s.PromptTokens)},
			{"completion_tokens", fmtNumInt(s.CompletionTokens)},
			{"cache_read", fmtNumInt(s.CacheReadTokens)},
			{"cache_write", fmtNumInt(s.CacheWriteTokens)},
			{"total_tokens", fmtNumInt(s.TotalTokens)},
		}
	}
	if c.info.Status.PID > 0 && ps.alive {
		return []kvPair{
			{"process_rss", fmt.Sprintf("%.1f MB", float64(ps.rssKB)/1024.0)},
			{"pid", strconv.Itoa(c.info.Status.PID)},
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
