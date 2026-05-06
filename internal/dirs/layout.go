// Package dirs defines the canonical on-disk layout for OpsIntelligence state.
//
// Single responsibility: every path the application reads or writes is derived
// from one Layout value so there are no scattered filepath.Join strings across
// the codebase. Callers depend on the Layout interface, not on raw strings.
package dirs

import (
	"os"
	"path/filepath"
	"runtime"
)

// Layout holds all resolved absolute paths for one OpsIntelligence state root.
// Construct via New; the zero value is invalid.
//
// Directory hierarchy:
//
//	<root>/
//	├── opsintelligence.yaml      main config (managed externally)
//	├── data/                     persistent databases and state
//	│   ├── ops.db
//	│   ├── memory/               episodic.db, semantic.db, mining_state.json
//	│   ├── mempalace/            world state + managed venv
//	│   ├── repointel/            repo intelligence store
//	│   └── localintel/           local inference cache
//	├── logs/                     structured operational logs
//	│   ├── agent/                runtrace.ndjson
//	│   ├── subagents/            sub-agent runtrace.ndjson
//	│   ├── pipeline/             completed pipeline traces
//	│   └── audit/                security audit.ndjson
//	├── runtime/                  ephemeral operational files
//	│   ├── opsintelligence.pid
//	│   ├── cron_jobs.json
//	│   ├── processes.json
//	│   └── channels/             DLQ and channel state
//	├── config/                   supplementary configuration
//	│   ├── teams/                team prompt directories
//	│   ├── tools/                custom tool definitions
//	│   └── prompts/              custom prompt files
//	├── identity/                 agent persona documents
//	├── models/                   ML model files (GGUF etc.)
//	├── skills/                   installed skills
//	│   ├── bundled/
//	│   └── custom/
//	├── security/                 policy files
//	├── venv/                     Python virtual environment (legacy alias)
//	└── workspace/                agent-generated outputs
//	    └── public/
type Layout struct {
	Root string

	// Data — persistent databases and long-lived state.
	Data        string
	Memory      string
	MemPalace   string
	RepoIntel   string
	RepoClones  string // shallow git clones for non-GitHub or token-less repos
	LocalIntel  string

	// Logs — structured operational logs.
	Logs          string
	LogsAgent     string
	LogsSubagents string
	LogsPipeline  string
	LogsAudit     string

	// Runtime — ephemeral operational files (PID, job state).
	Runtime  string
	Channels string

	// Config — supplementary configuration overlays.
	Config       string
	Teams        string
	Tools        string
	Prompts      string
	PRReview     string
	DevOps       string
	Agents       string // agent flow definitions per agent
	CustomAgents string // user-created agent definitions

	// Identity — agent persona and policy documents.
	Identity string

	// Models — ML model files.
	Models string

	// Skills — installed skills, bundled and custom.
	Skills        string
	SkillsBundled string
	SkillsCustom  string

	// Security — policy files (audit logs live in LogsAudit).
	Security string

	// Workspace — agent-generated outputs served over HTTP.
	Workspace       string
	WorkspacePublic string

	// VEnv — Python virtual environment.
	VEnv string
}

// New builds a Layout rooted at root (typically ~/.opsintelligence).
// All paths are absolute; no filesystem operations are performed.
func New(root string) *Layout {
	j := func(parts ...string) string {
		return filepath.Join(append([]string{root}, parts...)...)
	}
	return &Layout{
		Root:            root,
		Data:            j("data"),
		Memory:          j("data", "memory"),
		MemPalace:       j("data", "mempalace"),
		RepoIntel:       j("data", "repointel"),
		RepoClones:      j("data", "repointel", "clones"),
		LocalIntel:      j("data", "localintel"),
		Logs:            j("logs"),
		LogsAgent:       j("logs", "agent"),
		LogsSubagents:   j("logs", "subagents"),
		LogsPipeline:    j("logs", "pipeline"),
		LogsAudit:       j("logs", "audit"),
		Runtime:         j("runtime"),
		Channels:        j("runtime", "channels"),
		Config:          j("config"),
		Teams:           j("config", "teams"),
		Tools:           j("config", "tools"),
		Prompts:         j("config", "prompts"),
		PRReview:        j("config", "pr_review"),
		DevOps:          j("config", "devops"),
		Agents:          j("config", "agents"),
		CustomAgents:    j("config", "agents", "custom"),
		Identity:        j("identity"),
		Models:          j("models"),
		Skills:          j("skills"),
		SkillsBundled:   j("skills", "bundled"),
		SkillsCustom:    j("skills", "custom"),
		Security:        j("security"),
		Workspace:       j("workspace"),
		WorkspacePublic: j("workspace", "public"),
		VEnv:            j("venv"),
	}
}

// ── File paths ────────────────────────────────────────────────────────────────

func (l *Layout) PRReviewMethodology() string           { return filepath.Join(l.PRReview, "methodology.md") }
func (l *Layout) DevOpsWorkflow() string                { return filepath.Join(l.DevOps, "workflow.md") }
func (l *Layout) AgentFlowYAML(name string) string      { return filepath.Join(l.Agents, name, "flow.yaml") }
func (l *Layout) CustomAgentDefPath(name string) string { return filepath.Join(l.CustomAgents, name, "agent.yaml") }

func (l *Layout) OpsDB() string           { return filepath.Join(l.Data, "ops.db") }
func (l *Layout) EpisodicDB() string      { return filepath.Join(l.Memory, "episodic.db") }
func (l *Layout) SemanticDB() string      { return filepath.Join(l.Memory, "semantic.db") }
func (l *Layout) MiningState() string     { return filepath.Join(l.Memory, "mining_state.json") }
func (l *Layout) WhatsAppDB() string      { return filepath.Join(l.Data, "whatsapp.db") }
func (l *Layout) PidFile() string         { return filepath.Join(l.Runtime, "opsintelligence.pid") }
func (l *Layout) CronJobs() string        { return filepath.Join(l.Runtime, "cron_jobs.json") }
func (l *Layout) Processes() string       { return filepath.Join(l.Runtime, "processes.json") }
func (l *Layout) DLQ() string             { return filepath.Join(l.Channels, "dlq.ndjson") }
func (l *Layout) AuditLog() string        { return filepath.Join(l.LogsAudit, "audit.ndjson") }
func (l *Layout) AgentRunTrace() string   { return filepath.Join(l.LogsAgent, "runtrace.ndjson") }
func (l *Layout) SubagentRunTrace() string { return filepath.Join(l.LogsSubagents, "runtrace.ndjson") }

// MemPalaceVenv returns the managed Python venv path inside MemPalace.
func (l *Layout) MemPalaceVenv() string { return filepath.Join(l.MemPalace, "venv") }

// MemPalaceWorld returns the MemPalace world directory.
func (l *Layout) MemPalaceWorld() string { return filepath.Join(l.MemPalace, "world") }

// MemPalaceInitMarker is written after a successful `mempalace init`.
func (l *Layout) MemPalaceInitMarker() string {
	return filepath.Join(l.MemPalace, ".world_initialized")
}

// MemPalaceVenvInterpreter returns the Python interpreter inside the managed venv.
func (l *Layout) MemPalaceVenvInterpreter() string {
	venv := l.MemPalaceVenv()
	if runtime.GOOS == "windows" {
		return filepath.Join(venv, "Scripts", "python.exe")
	}
	p := filepath.Join(venv, "bin", "python3")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return filepath.Join(venv, "bin", "python")
}

// AllDirs returns every directory that must exist at startup, in creation order.
func (l *Layout) AllDirs() []string {
	return []string{
		l.Data,
		l.Memory,
		l.MemPalace,
		l.RepoIntel,
		l.RepoClones,
		l.LocalIntel,
		l.Logs,
		l.LogsAgent,
		l.LogsSubagents,
		l.LogsPipeline,
		l.LogsAudit,
		l.Runtime,
		l.Channels,
		l.Config,
		l.Teams,
		l.Tools,
		l.Prompts,
		l.PRReview,
		l.DevOps,
		l.Agents,
		l.CustomAgents,
		l.Identity,
		l.Models,
		l.Skills,
		l.SkillsBundled,
		l.SkillsCustom,
		l.Security,
		l.Workspace,
		l.WorkspacePublic,
	}
}

// EnsureAll creates all required directories with 0755 permissions.
// Existing directories are left unchanged.
func (l *Layout) EnsureAll() error {
	for _, dir := range l.AllDirs() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}
