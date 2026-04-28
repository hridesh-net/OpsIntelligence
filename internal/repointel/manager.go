package repointel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/embeddings"
	"go.uber.org/zap"
)

// ── ManagerConfig ─────────────────────────────────────────────────────────────

// ManagerConfig holds configuration for the Manager.
type ManagerConfig struct {
	// RegistryPath is the absolute path to the repos.yaml registry file.
	RegistryPath string

	// MemoryDir is the directory where per-repo memory and scan files are stored.
	MemoryDir string

	// ProgressBuf is the buffer size for the Progress channel. Default 64.
	ProgressBuf int

	// Embedder is the optional embedding provider used to build the vector
	// memory base after indexing. If nil, vector storage is disabled.
	Embedder embeddings.Embedder

	// EmbeddingDimensions must match Embedder's output dimension. Default 1536.
	EmbeddingDimensions int

	// PollInterval is how often Start() scans for newly-pending repos added via
	// CLI while the agent is already running. Default 60s.
	PollInterval time.Duration

	// MonitorInterval is how often Start() polls each indexed repo's HEAD SHA
	// and re-enqueues it when the remote has new commits. Default 6h.
	MonitorInterval time.Duration
}

func (c *ManagerConfig) applyDefaults() {
	if c.ProgressBuf <= 0 {
		c.ProgressBuf = 64
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 60 * time.Second
	}
	if c.MonitorInterval <= 0 {
		c.MonitorInterval = 6 * time.Hour
	}
	if c.EmbeddingDimensions <= 0 {
		c.EmbeddingDimensions = 1536
	}
}

// ── Manager ───────────────────────────────────────────────────────────────────

// Manager is the central coordinator for the Repo Intelligence feature.
//
// It owns the Registry, the Indexer, and the Scanner. When a repo is added
// (or manually re-synced), the Manager enqueues it for sequential processing:
// index first, then scan.  Progress events are streamed on the Progress channel.
type Manager struct {
	cfg      ManagerConfig
	registry *Registry
	indexer  *Indexer
	scanner  *Scanner
	vectors  *VectorStore // nil when no embedder configured
	log      *zap.Logger

	// workQueue receives repo IDs to process.
	workQueue chan string

	// Progress receives events from the active indexing/scanning run.
	Progress chan ProgressEvent

	mu      sync.Mutex
	running bool
}

// NewManager constructs and initialises a Manager.
// Call Start(ctx) in a goroutine to begin processing.
func NewManager(
	cfg ManagerConfig,
	indexer *Indexer,
	scanner *Scanner,
	log *zap.Logger,
) (*Manager, error) {
	cfg.applyDefaults()
	reg, err := NewRegistry(cfg.RegistryPath)
	if err != nil {
		return nil, fmt.Errorf("repointel manager: %w", err)
	}

	// Open vector store when an embedder is available.
	var vs *VectorStore
	if cfg.Embedder != nil {
		vsPath := filepath.Join(cfg.MemoryDir, "repointel.db")
		vs, err = newVectorStore(vsPath, cfg.EmbeddingDimensions)
		if err != nil {
			if log != nil {
				log.Warn("repointel: vector store init failed; semantic search disabled", zap.Error(err))
			}
			vs = nil
		} else if log != nil {
			log.Info("repointel: vector store ready", zap.String("path", vsPath), zap.Int("dims", cfg.EmbeddingDimensions))
		}
	}

	return &Manager{
		cfg:       cfg,
		registry:  reg,
		indexer:   indexer,
		scanner:   scanner,
		vectors:   vs,
		log:       log,
		workQueue: make(chan string, 256),
		Progress:  make(chan ProgressEvent, cfg.ProgressBuf),
	}, nil
}

// ── Lifecycle ─────────────────────────────────────────────────────────────────

// Start runs the sequential processing loop. Call in a goroutine; returns
// when ctx is cancelled.
//
// On startup it immediately enqueues any repos that are still pending (e.g.,
// added via CLI before the agent started).  It then polls for newly-pending
// repos every PollInterval and re-checks HEAD SHAs every MonitorInterval.
func (m *Manager) Start(ctx context.Context) {
	if m.log != nil {
		m.log.Info("repointel manager started")
	}

	// Bootstrap: enqueue all repos that are pending from a previous CLI add or
	// an interrupted processing run.
	m.enqueuePending()

	pollTick := time.NewTicker(m.cfg.PollInterval)
	monitorTick := time.NewTicker(m.cfg.MonitorInterval)
	defer pollTick.Stop()
	defer monitorTick.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case id := <-m.workQueue:
			m.process(ctx, id)

		case <-pollTick.C:
			// Pick up repos added via CLI while the agent is running.
			m.enqueuePending()

		case <-monitorTick.C:
			// Re-index repos whose HEAD SHA has changed since last index.
			m.enqueueChanged(ctx)
		}
	}
}

// enqueuePending enqueues every repo that is in a pending index or scan state.
func (m *Manager) enqueuePending() {
	entries := m.registry.List()
	enqueued := 0
	for _, e := range entries {
		if e.IndexStatus == IndexPending || e.ScanStatus == ScanPending {
			m.enqueue(e.ID)
			enqueued++
		}
	}
	if enqueued > 0 && m.log != nil {
		m.log.Info("repointel: enqueued pending repos", zap.Int("count", enqueued))
	}
}

// enqueueChanged checks HEAD SHA for all indexed repos and re-enqueues those
// that have new commits since the last index.
func (m *Manager) enqueueChanged(ctx context.Context) {
	entries := m.registry.List()
	for _, e := range entries {
		if e.IndexStatus != IndexReady {
			continue
		}
		current, err := m.indexer.CurrentHeadSHA(ctx, e)
		if err != nil {
			if m.log != nil {
				m.log.Warn("repointel: head SHA check failed", zap.String("repo", e.ID), zap.Error(err))
			}
			continue
		}
		if current != "" && current != e.HeadSHA {
			if m.log != nil {
				m.log.Info("repointel: repo has new commits, scheduling re-index",
					zap.String("repo", e.ID),
					zap.String("old_sha", e.HeadSHA[:min8(e.HeadSHA)]),
					zap.String("new_sha", current[:min8(current)]),
				)
			}
			_ = m.registry.UpdateIndexStatus(e.ID, IndexPending, "", "")
			_ = m.registry.UpdateScanStatus(e.ID, ScanPending, "", "")
			m.enqueue(e.ID)
		}
	}
}

func min8(s string) int {
	if len(s) < 8 {
		return len(s)
	}
	return 8
}

// ── Public API ────────────────────────────────────────────────────────────────

// AddRepo registers a new repo and immediately enqueues it for indexing and scanning.
func (m *Manager) AddRepo(entry RepoEntry) error {
	if err := m.registry.Add(entry); err != nil {
		return err
	}
	m.enqueue(entry.ID)
	return nil
}

// RemoveRepo removes the repo from the registry and its vector store entry.
func (m *Manager) RemoveRepo(id string) error {
	_ = m.vectors.Delete(id)
	return m.registry.Remove(id)
}

// SearchRepos performs a semantic similarity search over all indexed repo
// memories. Returns up to k results ordered by relevance.
// Returns nil when the vector store is not configured.
func (m *Manager) SearchRepos(ctx context.Context, query string, k int) ([]VectorSearchResult, error) {
	if m.vectors == nil || m.cfg.Embedder == nil {
		return nil, nil
	}
	resp, err := m.cfg.Embedder.Embed(ctx, &embeddings.EmbedRequest{
		Model:     m.cfg.Embedder.DefaultModel(),
		Texts:     []string{query},
		InputType: embeddings.InputTypeQuery,
	})
	if err != nil {
		return nil, fmt.Errorf("search repos: embed query: %w", err)
	}
	if len(resp.Embeddings) == 0 {
		return nil, nil
	}
	return m.vectors.Search(resp.Embeddings[0], k)
}

// GetRepo returns the registry entry for id.
func (m *Manager) GetRepo(id string) (RepoEntry, error) {
	return m.registry.Get(id)
}

// ListRepos returns all registered repos.
func (m *Manager) ListRepos() []RepoEntry {
	return m.registry.List()
}

// SyncRepo re-enqueues an existing repo for a fresh index+scan.
func (m *Manager) SyncRepo(id string) error {
	if _, err := m.registry.Get(id); err != nil {
		return err
	}
	m.enqueue(id)
	return nil
}

// AddUser adds or replaces a user on the repo.
func (m *Manager) AddUser(repoID string, u RepoUser) error {
	return m.registry.AddUser(repoID, u)
}

// RemoveUser removes a user from the repo.
func (m *Manager) RemoveUser(repoID, handle string) error {
	return m.registry.RemoveUser(repoID, handle)
}

// LoadMemory reads the persisted RepoMemory for a repo.
// Returns nil, nil if the repo has not yet been indexed.
func (m *Manager) LoadMemory(id string) (*RepoMemory, error) {
	entry, err := m.registry.Get(id)
	if err != nil {
		return nil, err
	}
	if entry.MemoryFile == "" || entry.IndexStatus != IndexReady {
		return nil, nil
	}
	path := filepath.Join(m.cfg.MemoryDir, entry.MemoryFile)
	return LoadMemory(path)
}

// LoadScan reads the persisted ScanResult for a repo.
// Returns nil, nil if the repo has not yet been scanned.
func (m *Manager) LoadScan(id string) (*ScanResult, error) {
	entry, err := m.registry.Get(id)
	if err != nil {
		return nil, err
	}
	if entry.ScanFile == "" || entry.ScanStatus != ScanDone {
		return nil, nil
	}
	path := filepath.Join(m.cfg.MemoryDir, entry.ScanFile)
	return LoadScan(path)
}

// MemoryForReview returns the ReviewContext markdown for a repo, or "" if not
// yet indexed. Intended for injection into PR review prompts.
func (m *Manager) MemoryForReview(id string) string {
	mem, err := m.LoadMemory(id)
	if err != nil || mem == nil {
		return ""
	}
	return mem.ReviewContext()
}

// WithContext returns a new context with this Manager attached.
func (m *Manager) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, ManagerKey, m)
}

// ── Internal processing ───────────────────────────────────────────────────────

func (m *Manager) enqueue(id string) {
	select {
	case m.workQueue <- id:
	default:
		if m.log != nil {
			m.log.Warn("repointel work queue full, dropping sync request",
				zap.String("repo", id))
		}
	}
}

func (m *Manager) emit(e ProgressEvent) {
	select {
	case m.Progress <- e:
	default:
		// Drop progress event if buffer full — non-fatal.
	}
}

// process runs index → scan sequentially for one repo.
func (m *Manager) process(ctx context.Context, id string) {
	entry, err := m.registry.Get(id)
	if err != nil {
		m.emit(ProgressEvent{RepoID: id, Kind: ProgressError, Message: err.Error(), Error: err})
		return
	}

	// ── Index ──────────────────────────────────────────────────────────────────
	m.emit(ProgressEvent{RepoID: id, Kind: ProgressIndexing, Message: "indexing codebase"})
	_ = m.registry.UpdateIndexStatus(id, IndexIndexing, "", "")

	mem, err := m.indexer.Index(ctx, entry)
	if err != nil {
		_ = m.registry.UpdateIndexStatus(id, IndexError, "", err.Error())
		m.emit(ProgressEvent{RepoID: id, Kind: ProgressError, Message: "indexing failed: " + err.Error(), Error: err})
		return
	}

	// Persist memory file.
	relPath := sanitiseID(id) + "-memory.json"
	absPath := filepath.Join(m.cfg.MemoryDir, relPath)
	if saveErr := SaveMemory(absPath, mem); saveErr != nil {
		_ = m.registry.UpdateIndexStatus(id, IndexError, "", saveErr.Error())
		m.emit(ProgressEvent{RepoID: id, Kind: ProgressError, Message: "save memory: " + saveErr.Error(), Error: saveErr})
		return
	}
	_ = m.registry.SetMemoryFile(id, relPath)
	_ = m.registry.UpdateIndexStatus(id, IndexReady, mem.HeadSHA, "")
	if mem.PrimaryLang != "" {
		_ = m.registry.UpdateMetadata(id, "", mem.PrimaryLang)
	}

	// ── Scan ───────────────────────────────────────────────────────────────────
	m.emit(ProgressEvent{RepoID: id, Kind: ProgressScanning, Message: "scanning for CVEs and bottlenecks"})
	_ = m.registry.UpdateScanStatus(id, ScanScanning, "", "")

	// Re-fetch entry so we have the updated MemoryFile path.
	entry, _ = m.registry.Get(id)

	scanResult, err := m.scanner.Scan(ctx, entry, mem)
	if err != nil {
		_ = m.registry.UpdateScanStatus(id, ScanError, "", err.Error())
		m.emit(ProgressEvent{RepoID: id, Kind: ProgressError, Message: "scan failed: " + err.Error(), Error: err})
		return
	}

	// Persist scan file.
	scanRel := sanitiseID(id) + "-scan.json"
	scanAbs := filepath.Join(m.cfg.MemoryDir, scanRel)
	if saveErr := SaveScan(scanAbs, scanResult); saveErr != nil {
		_ = m.registry.UpdateScanStatus(id, ScanError, "", saveErr.Error())
		m.emit(ProgressEvent{RepoID: id, Kind: ProgressError, Message: "save scan: " + saveErr.Error(), Error: saveErr})
		return
	}
	_ = m.registry.SetScanFile(id, scanRel)
	_ = m.registry.UpdateScanStatus(id, ScanDone, scanResult.RiskLevel, "")

	// ── Vector memory base ─────────────────────────────────────────────────────
	// Store a merged document (memory + scan summary) in the sqlite-vec store
	// so agents can later search repos by semantic similarity.
	m.indexVectors(ctx, id, mem, scanResult)

	m.emit(ProgressEvent{
		RepoID:  id,
		Kind:    ProgressDone,
		Message: fmt.Sprintf("done — risk=%s cves=%d bottlenecks=%d", scanResult.RiskLevel, len(scanResult.CVEs), len(scanResult.Bottlenecks)),
	})

	if m.log != nil {
		m.log.Info("repointel processing complete",
			zap.String("repo", id),
			zap.String("risk", scanResult.RiskLevel),
			zap.Int("cves", len(scanResult.CVEs)),
		)
	}
}

// indexVectors embeds the merged repo document and stores it in the vector store.
// Silently skips when no embedder or vector store is configured.
func (m *Manager) indexVectors(ctx context.Context, id string, mem *RepoMemory, scan *ScanResult) {
	if m.vectors == nil || m.cfg.Embedder == nil {
		return
	}

	doc := buildVectorDoc(mem, scan)
	if doc == "" {
		return
	}

	resp, err := m.cfg.Embedder.Embed(ctx, &embeddings.EmbedRequest{
		Model:     m.cfg.Embedder.DefaultModel(),
		Texts:     []string{doc},
		InputType: embeddings.InputTypeDocument,
	})
	if err != nil {
		if m.log != nil {
			m.log.Warn("repointel: embed failed; skipping vector store", zap.String("repo", id), zap.Error(err))
		}
		return
	}
	if len(resp.Embeddings) == 0 {
		return
	}

	if err := m.vectors.Upsert(id, doc, resp.Embeddings[0]); err != nil {
		if m.log != nil {
			m.log.Warn("repointel: vector store upsert failed", zap.String("repo", id), zap.Error(err))
		}
		return
	}
	if m.log != nil {
		m.log.Info("repointel: vector memory stored", zap.String("repo", id))
	}
}

// buildVectorDoc merges memory + scan result into a searchable text document.
func buildVectorDoc(mem *RepoMemory, scan *ScanResult) string {
	if mem == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Repo: " + mem.RepoID + "\n")
	if mem.Architecture != "" {
		sb.WriteString("Architecture: " + mem.Architecture + "\n")
	}
	if mem.PrimaryLang != "" {
		sb.WriteString("Language: " + mem.PrimaryLang + "\n")
	}
	if mem.ReviewHints != "" {
		sb.WriteString("Review hints: " + mem.ReviewHints + "\n")
	}
	for _, c := range mem.CommonIssues {
		sb.WriteString("Issue: " + c + "\n")
	}
	if scan != nil {
		sb.WriteString("Risk: " + scan.RiskLevel + "\n")
		if scan.Summary != "" {
			sb.WriteString("Scan summary: " + scan.Summary + "\n")
		}
		for _, cve := range scan.CVEs {
			sb.WriteString("CVE: " + cve.Package + " " + cve.Severity + " " + cve.Description + "\n")
		}
		for _, b := range scan.Bottlenecks {
			sb.WriteString("Bottleneck: " + b.Location + " " + b.Description + "\n")
		}
	}
	return sb.String()
}

// ── Stats ─────────────────────────────────────────────────────────────────────

// RepoStats is a summary of a repo's current state for display purposes.
type RepoStats struct {
	Entry        RepoEntry
	CVECount     int
	BottleCount  int
	Suggestions  int
	RiskLevel    string
	LastIndexed  time.Time
	LastScanned  time.Time
}

// Stats returns a RepoStats snapshot for one repo.
func (m *Manager) Stats(id string) (RepoStats, error) {
	entry, err := m.registry.Get(id)
	if err != nil {
		return RepoStats{}, err
	}
	rs := RepoStats{
		Entry:       entry,
		RiskLevel:   entry.RiskLevel,
		LastIndexed: entry.IndexedAt,
		LastScanned: entry.ScannedAt,
	}
	if scan, _ := m.LoadScan(id); scan != nil {
		rs.CVECount = len(scan.CVEs)
		rs.BottleCount = len(scan.Bottlenecks)
		rs.Suggestions = len(scan.Suggestions)
	}
	return rs, nil
}

// AllStats returns RepoStats for every registered repo.
func (m *Manager) AllStats() []RepoStats {
	entries := m.registry.List()
	out := make([]RepoStats, 0, len(entries))
	for _, e := range entries {
		rs, _ := m.Stats(e.ID)
		out = append(out, rs)
	}
	return out
}
