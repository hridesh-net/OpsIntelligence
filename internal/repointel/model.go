// Package repointel implements the Repo Intelligence system:
// configuring repos, indexing their codebases, running CVE and
// bottleneck scans, managing per-repo users, and injecting that
// knowledge into PR reviews and pipeline analysis.
package repointel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ── Status types ─────────────────────────────────────────────────────────────

// IndexStatus tracks the codebase indexing lifecycle.
type IndexStatus string

const (
	IndexPending  IndexStatus = "pending"
	IndexIndexing IndexStatus = "indexing"
	IndexReady    IndexStatus = "ready"
	IndexError    IndexStatus = "error"
)

// ScanStatus tracks the CVE / bottleneck scan lifecycle.
type ScanStatus string

const (
	ScanPending  ScanStatus = "pending"
	ScanScanning ScanStatus = "scanning"
	ScanDone     ScanStatus = "done"
	ScanError    ScanStatus = "error"
)

// UserRole defines a user's access level for a repo.
type UserRole string

const (
	RoleAdmin       UserRole = "admin"
	RoleMaintainer  UserRole = "maintainer"
	RoleReviewer    UserRole = "reviewer"
	RoleContributor UserRole = "contributor"
)

// ── Registry model ────────────────────────────────────────────────────────────

// RepoUser associates a platform user handle with a role in this repo.
type RepoUser struct {
	Handle  string    `yaml:"handle" json:"handle"`
	Role    UserRole  `yaml:"role" json:"role"`
	Email   string    `yaml:"email,omitempty" json:"email,omitempty"`
	AddedAt time.Time `yaml:"added_at" json:"added_at"`
}

// RepoEntry is the registry record for one configured repo.
// Stored in the repos.yaml registry file.
type RepoEntry struct {
	// Identity
	ID       string `yaml:"id" json:"id"`             // "github:owner/name"
	Platform string `yaml:"platform" json:"platform"` // "github" | "gitlab"
	Owner    string `yaml:"owner" json:"owner"`
	Name     string `yaml:"name" json:"name"`
	FullName string `yaml:"full_name" json:"full_name"` // "owner/name"
	CloneURL string `yaml:"clone_url,omitempty" json:"clone_url,omitempty"`

	// Metadata (populated during indexing)
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Language    string `yaml:"language,omitempty" json:"language,omitempty"`

	// Indexing state
	IndexStatus IndexStatus `yaml:"index_status" json:"index_status"`
	IndexedAt   time.Time   `yaml:"indexed_at,omitempty" json:"indexed_at,omitempty"`
	HeadSHA     string      `yaml:"head_sha,omitempty" json:"head_sha,omitempty"`
	IndexError  string      `yaml:"index_error,omitempty" json:"index_error,omitempty"`

	// Scan state
	ScanStatus ScanStatus `yaml:"scan_status" json:"scan_status"`
	ScannedAt  time.Time  `yaml:"scanned_at,omitempty" json:"scanned_at,omitempty"`
	ScanError  string     `yaml:"scan_error,omitempty" json:"scan_error,omitempty"`
	RiskLevel  string     `yaml:"risk_level,omitempty" json:"risk_level,omitempty"` // critical|high|medium|low|info

	// Users
	Users []RepoUser `yaml:"users,omitempty" json:"users,omitempty"`

	// Storage paths (relative to MemoryDir)
	MemoryFile    string `yaml:"memory_file,omitempty" json:"memory_file,omitempty"`
	ScanFile      string `yaml:"scan_file,omitempty" json:"scan_file,omitempty"`
	RefMDFile     string `yaml:"ref_md_file,omitempty" json:"ref_md_file,omitempty"`         // human-readable reference
	SummaryMDFile string `yaml:"summary_md_file,omitempty" json:"summary_md_file,omitempty"` // LLM-compact summary
	CallGraphFile string `yaml:"call_graph_file,omitempty" json:"call_graph_file,omitempty"` // function call graph JSON

	AddedAt time.Time `yaml:"added_at" json:"added_at"`
}

// RepoID generates a canonical ID for a repo.
func RepoID(platform, owner, name string) string {
	return fmt.Sprintf("%s:%s/%s", platform, owner, name)
}

// ── Repo memory ───────────────────────────────────────────────────────────────

// CodeConvention is a recurring coding pattern observed in the repo.
type CodeConvention struct {
	Name    string `json:"name"`    // e.g. "error wrapping"
	Pattern string `json:"pattern"` // description of the convention
}

// Dependency is a key dependency extracted from manifests.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Purpose string `json:"purpose,omitempty"`
}

// RepoMemory is the learned structural knowledge about a repo's codebase.
// Produced by the Indexer and stored as a JSON file per repo.
type RepoMemory struct {
	RepoID    string    `json:"repo_id"`
	UpdatedAt time.Time `json:"updated_at"`
	HeadSHA   string    `json:"head_sha,omitempty"`

	// LLM-extracted knowledge
	Architecture string           `json:"architecture"`  // high-level overview
	PrimaryLang  string           `json:"primary_lang"`
	Languages    []string         `json:"languages,omitempty"`
	KeyFiles     []string         `json:"key_files,omitempty"`     // important entry points
	Conventions  []CodeConvention `json:"conventions,omitempty"`
	Dependencies []Dependency     `json:"dependencies,omitempty"`
	TestPatterns string           `json:"test_patterns,omitempty"` // how tests are structured
	CISummary    string           `json:"ci_summary,omitempty"`    // CI/CD description
	ReviewHints  string           `json:"review_hints,omitempty"`  // what reviewers should focus on
	CommonIssues []string         `json:"common_issues,omitempty"` // patterns that cause bugs

	// UserContext is free-form context provided by the operator to augment or
	// correct the LLM's understanding. Injected into PR review prompts alongside
	// the LLM-extracted fields.
	UserContext string `json:"user_context,omitempty"`

	// RawFiles holds the raw file contents fetched during indexing.
	// Not persisted to JSON — populated by the Indexer for chunking / MD gen.
	RawFiles []RawFile `json:"-"`
}

// RawFile is one source file fetched from the repo during indexing.
type RawFile struct {
	Path    string
	Content string // may be truncated at 8 KiB
}

// ReviewContext returns a compact markdown block for injection into PR review prompts.
func (m *RepoMemory) ReviewContext() string {
	if m == nil {
		return ""
	}
	out := "## Repo Intelligence Context\n\n"
	if m.Architecture != "" {
		out += "**Architecture:** " + m.Architecture + "\n\n"
	}
	if m.PrimaryLang != "" {
		out += "**Primary language:** " + m.PrimaryLang + "\n"
	}
	if len(m.Conventions) > 0 {
		out += "\n**Coding conventions:**\n"
		for _, c := range m.Conventions {
			out += "- " + c.Name + ": " + c.Pattern + "\n"
		}
	}
	if m.ReviewHints != "" {
		out += "\n**Review focus:** " + m.ReviewHints + "\n"
	}
	if len(m.CommonIssues) > 0 {
		out += "\n**Common issue patterns to watch for:**\n"
		for _, issue := range m.CommonIssues {
			out += "- " + issue + "\n"
		}
	}
	if m.TestPatterns != "" {
		out += "\n**Test patterns:** " + m.TestPatterns + "\n"
	}
	if m.UserContext != "" {
		out += "\n**Operator notes:** " + m.UserContext + "\n"
	}
	return out
}

// ── Scan result ───────────────────────────────────────────────────────────────

// CVEFinding is a known or suspected CVE in a dependency.
type CVEFinding struct {
	Severity    string   `json:"severity"`              // critical|high|medium|low
	Package     string   `json:"package"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description"`
	Fix         string   `json:"fix,omitempty"`         // recommended action
	CVEIDs      []string `json:"cve_ids,omitempty"`     // e.g. ["CVE-2024-1234"]
}

// BottleneckFinding is a performance or reliability risk in the codebase.
type BottleneckFinding struct {
	Severity    string `json:"severity"` // high|medium|low
	Location    string `json:"location"` // file path or component
	Description string `json:"description"`
	Fix         string `json:"fix,omitempty"`
}

// ArchitectureSuggestion is a structural improvement recommendation.
type ArchitectureSuggestion struct {
	Priority   string `json:"priority"` // high|medium|low
	Area       string `json:"area"`
	Suggestion string `json:"suggestion"`
}

// ScanResult holds all findings from a security and bottleneck scan.
type ScanResult struct {
	RepoID    string    `json:"repo_id"`
	ScannedAt time.Time `json:"scanned_at"`

	RiskLevel   string                   `json:"risk_level"` // critical|high|medium|low|info
	Summary     string                   `json:"summary"`
	CVEs        []CVEFinding             `json:"cves,omitempty"`
	Bottlenecks []BottleneckFinding      `json:"bottlenecks,omitempty"`
	Suggestions []ArchitectureSuggestion `json:"suggestions,omitempty"`
}

// HasCritical returns true if any CVE is critical severity.
func (s *ScanResult) HasCritical() bool {
	for _, c := range s.CVEs {
		if c.Severity == "critical" {
			return true
		}
	}
	return false
}

// ── JSON persistence helpers ──────────────────────────────────────────────────

// SaveMemory writes m to path as formatted JSON.
func SaveMemory(path string, m *RepoMemory) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// LoadMemory reads a RepoMemory from path.
func LoadMemory(path string) (*RepoMemory, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m RepoMemory
	return &m, json.Unmarshal(b, &m)
}

// SaveScan writes s to path as formatted JSON.
func SaveScan(path string, s *ScanResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// LoadScan reads a ScanResult from path.
func LoadScan(path string) (*ScanResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s ScanResult
	return &s, json.Unmarshal(b, &s)
}

// MemoryPath returns the standard path for a repo's memory file.
func MemoryPath(memoryDir, repoID string) string {
	return filepath.Join(memoryDir, sanitiseID(repoID)+"-memory.json")
}

// ScanPath returns the standard path for a repo's scan result file.
func ScanPath(memoryDir, repoID string) string {
	return filepath.Join(memoryDir, sanitiseID(repoID)+"-scan.json")
}

// SanitiseID converts a repo ID into a filesystem-safe string.
func SanitiseID(id string) string { return sanitiseID(id) }

func sanitiseID(id string) string {
	out := make([]byte, 0, len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '-')
		}
	}
	return string(out)
}

// ── Progress event ────────────────────────────────────────────────────────────

// ProgressKind classifies a progress event.
type ProgressKind string

const (
	ProgressIndexing ProgressKind = "indexing"
	ProgressScanning ProgressKind = "scanning"
	ProgressDone     ProgressKind = "done"
	ProgressError    ProgressKind = "error"
)

// ProgressEvent is emitted by the Manager to report indexing/scan progress.
type ProgressEvent struct {
	RepoID  string
	Kind    ProgressKind
	Message string
	// Step and Total allow UIs to compute a percentage. Step is 1-based.
	Step  int
	Total int
	Error error
}

// Pct returns 0–100 completion percentage, or -1 if unknown.
func (p ProgressEvent) Pct() int {
	if p.Total <= 0 {
		return -1
	}
	v := (p.Step * 100) / p.Total
	if v > 100 {
		return 100
	}
	return v
}

// contextKey is used for context values.
type contextKey struct{ name string }

// managerKey is the context key for injecting a Manager into callers.
var ManagerKey = &contextKey{"repointel.manager"}

// FromContext retrieves a Manager from ctx, or nil.
func FromContext(ctx context.Context) *Manager {
	if v := ctx.Value(ManagerKey); v != nil {
		if m, ok := v.(*Manager); ok {
			return m
		}
	}
	return nil
}
