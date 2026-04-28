package repointel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/embeddings"
	"go.uber.org/zap"
)

// Ensure embeddings import is used (suppress lint false-positive for IDE).
var _ = embeddings.InputTypeDocument

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
// index first, then scan, then chunk+embed into the hybrid store and generate
// markdown files.  Progress events are streamed on the Progress channel.
type Manager struct {
	cfg      ManagerConfig
	registry *Registry
	indexer  *Indexer
	scanner  *Scanner
	hybrid   *HybridStore // nil when no embedder configured
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

	// Open hybrid store when an embedder is available.
	var hs *HybridStore
	if cfg.Embedder != nil {
		hsPath := filepath.Join(cfg.MemoryDir, "repointel.db")
		hs, err = NewHybridStore(hsPath, cfg.EmbeddingDimensions)
		if err != nil {
			if log != nil {
				log.Warn("repointel: hybrid store init failed; search disabled", zap.Error(err))
			}
			hs = nil
		} else if log != nil {
			log.Info("repointel: hybrid store ready",
				zap.String("path", hsPath),
				zap.Int("dims", cfg.EmbeddingDimensions))
		}
	}

	return &Manager{
		cfg:       cfg,
		registry:  reg,
		indexer:   indexer,
		scanner:   scanner,
		hybrid:    hs,
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

// RemoveRepo removes the repo from the registry and clears its hybrid store entries.
func (m *Manager) RemoveRepo(id string) error {
	_ = m.hybrid.DeleteRepo(id)
	return m.registry.Remove(id)
}

// SearchRepos performs a hybrid (keyword + semantic) search over all indexed
// repo chunks. Returns up to k results ordered by RRF score.
// Returns nil when hybrid store is not configured.
func (m *Manager) SearchRepos(ctx context.Context, query string, k int) ([]HybridResult, error) {
	if m.hybrid == nil {
		return nil, nil
	}
	var queryVec []float32
	if m.cfg.Embedder != nil && query != "" {
		resp, err := m.cfg.Embedder.Embed(ctx, &embeddings.EmbedRequest{
			Model:     m.cfg.Embedder.DefaultModel(),
			Texts:     []string{query},
			InputType: embeddings.InputTypeQuery,
		})
		if err == nil && len(resp.Embeddings) > 0 {
			queryVec = resp.Embeddings[0]
		}
	}
	return m.hybrid.Search(query, queryVec, k)
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

// LoadCallGraph reads the persisted CallGraph for a repo.
// Returns nil, nil if the repo has not been indexed or graph was not generated.
func (m *Manager) LoadCallGraph(id string) (*CallGraph, error) {
	entry, err := m.registry.Get(id)
	if err != nil {
		return nil, err
	}
	if entry.CallGraphFile == "" {
		return nil, nil
	}
	path := filepath.Join(m.cfg.MemoryDir, entry.CallGraphFile)
	return LoadCallGraph(path)
}

// CallGraphHTMLPath returns the absolute path to the exported call graph HTML
// for the given repo, or "" if no call graph has been generated.
func (m *Manager) CallGraphHTMLPath(id string) string {
	entry, err := m.registry.Get(id)
	if err != nil || entry.CallGraphFile == "" {
		return ""
	}
	base := strings.TrimSuffix(entry.CallGraphFile, "-callgraph.json")
	return filepath.Join(m.cfg.MemoryDir, base+"-callgraph.html")
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
	// Also persist to progress.json so out-of-process TUI instances can read it.
	m.writeProgressFile(e)
}

// writeProgressFile updates the shared progress.json in MemoryDir.
// Written on best-effort — failures are silently ignored (display-only data).
func (m *Manager) writeProgressFile(e ProgressEvent) {
	path := filepath.Join(m.cfg.MemoryDir, "progress.json")

	// Read existing file (another repo may be tracked).
	state := map[string]ProgressEvent{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &state)
	}

	if e.Kind == ProgressDone || e.Kind == ProgressError {
		// Keep the final event so TUI can display it, then let next poll clear it.
		state[e.RepoID] = e
	} else {
		state[e.RepoID] = e
	}

	b, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = os.MkdirAll(m.cfg.MemoryDir, 0o755)
	_ = os.WriteFile(path, b, 0o644)
}

// pipelineSteps is the total number of steps in the processing pipeline.
// Used by the TUI to render a progress bar.
const pipelineSteps = 6

// process runs index → scan sequentially for one repo.
func (m *Manager) process(ctx context.Context, id string) {
	step := func(n int, kind ProgressKind, msg string) {
		m.emit(ProgressEvent{RepoID: id, Kind: kind, Message: msg, Step: n, Total: pipelineSteps})
	}
	fail := func(n int, msg string, err error) {
		m.emit(ProgressEvent{RepoID: id, Kind: ProgressError, Message: msg, Step: n, Total: pipelineSteps, Error: err})
	}

	entry, err := m.registry.Get(id)
	if err != nil {
		fail(0, err.Error(), err)
		return
	}

	// ── Step 1: Index codebase ────────────────────────────────────────────────
	step(1, ProgressIndexing, "fetching and analysing codebase")
	_ = m.registry.UpdateIndexStatus(id, IndexIndexing, "", "")

	mem, err := m.indexer.Index(ctx, entry)
	if err != nil {
		_ = m.registry.UpdateIndexStatus(id, IndexError, "", err.Error())
		fail(1, "indexing failed: "+err.Error(), err)
		return
	}

	// Persist memory file.
	relPath := sanitiseID(id) + "-memory.json"
	absPath := filepath.Join(m.cfg.MemoryDir, relPath)
	if saveErr := SaveMemory(absPath, mem); saveErr != nil {
		_ = m.registry.UpdateIndexStatus(id, IndexError, "", saveErr.Error())
		fail(1, "save memory: "+saveErr.Error(), saveErr)
		return
	}
	_ = m.registry.SetMemoryFile(id, relPath)
	_ = m.registry.UpdateIndexStatus(id, IndexReady, mem.HeadSHA, "")
	if mem.PrimaryLang != "" {
		_ = m.registry.UpdateMetadata(id, "", mem.PrimaryLang)
	}

	// ── Step 2: Scan for CVEs and bottlenecks ────────────────────────────────
	step(2, ProgressScanning, "scanning for CVEs and bottlenecks")
	_ = m.registry.UpdateScanStatus(id, ScanScanning, "", "")

	entry, _ = m.registry.Get(id)

	scanResult, err := m.scanner.Scan(ctx, entry, mem)
	if err != nil {
		_ = m.registry.UpdateScanStatus(id, ScanError, "", err.Error())
		fail(2, "scan failed: "+err.Error(), err)
		return
	}

	scanRel := sanitiseID(id) + "-scan.json"
	scanAbs := filepath.Join(m.cfg.MemoryDir, scanRel)
	if saveErr := SaveScan(scanAbs, scanResult); saveErr != nil {
		_ = m.registry.UpdateScanStatus(id, ScanError, "", saveErr.Error())
		fail(2, "save scan: "+saveErr.Error(), saveErr)
		return
	}
	_ = m.registry.SetScanFile(id, scanRel)
	_ = m.registry.UpdateScanStatus(id, ScanDone, scanResult.RiskLevel, "")

	// ── Step 3: Generate markdown reference files ─────────────────────────────
	step(3, ProgressIndexing, "generating reference docs (ref.md, summary.md)")
	entry, _ = m.registry.Get(id)
	m.generateMarkdown(id, entry, mem, scanResult)

	// ── Step 4: Build function call graph ─────────────────────────────────────
	step(4, ProgressIndexing, "building function call graph")
	m.buildCallGraph(id, mem)

	// ── Step 5: Hybrid search index (FTS5 + vec0) ─────────────────────────────
	step(5, ProgressIndexing, "indexing into hybrid search store")
	m.indexHybrid(ctx, id, mem, scanResult)

	// ── Step 6: Done ──────────────────────────────────────────────────────────
	doneMsg := fmt.Sprintf("done — risk=%s  CVEs=%d  bottlenecks=%d", scanResult.RiskLevel, len(scanResult.CVEs), len(scanResult.Bottlenecks))
	step(6, ProgressDone, doneMsg)

	if m.log != nil {
		m.log.Info("repointel processing complete",
			zap.String("repo", id),
			zap.String("risk", scanResult.RiskLevel),
			zap.Int("cves", len(scanResult.CVEs)),
		)
	}
}

// generateMarkdown writes ref.md and summary.md for the repo and records their
// paths in the registry.
func (m *Manager) generateMarkdown(id string, entry RepoEntry, mem *RepoMemory, scan *ScanResult) {
	base := sanitiseID(id)

	refRel := base + "-ref.md"
	refAbs := filepath.Join(m.cfg.MemoryDir, refRel)
	refContent := GenerateRefMD(entry, mem, scan)
	if err := SaveRefMD(refAbs, refContent); err != nil {
		if m.log != nil {
			m.log.Warn("repointel: save ref.md failed", zap.String("repo", id), zap.Error(err))
		}
	} else {
		_ = m.registry.SetRefMDFile(id, refRel)
	}

	sumRel := base + "-summary.md"
	sumAbs := filepath.Join(m.cfg.MemoryDir, sumRel)
	sumContent := GenerateSummaryMD(entry, mem, scan)
	if err := SaveSummaryMD(sumAbs, sumContent); err != nil {
		if m.log != nil {
			m.log.Warn("repointel: save summary.md failed", zap.String("repo", id), zap.Error(err))
		}
	} else {
		_ = m.registry.SetSummaryMDFile(id, sumRel)
		if m.log != nil {
			m.log.Info("repointel: markdown files written", zap.String("repo", id))
		}
	}
}

// buildCallGraph extracts the function call graph from raw files and persists it.
func (m *Manager) buildCallGraph(id string, mem *RepoMemory) {
	if len(mem.RawFiles) == 0 {
		return
	}
	cg := BuildCallGraph(id, mem.RawFiles)
	if len(cg.Nodes) == 0 {
		return
	}

	base := sanitiseID(id)
	cgRel := base + "-callgraph.json"
	cgAbs := filepath.Join(m.cfg.MemoryDir, cgRel)
	if err := SaveCallGraph(cgAbs, cg); err != nil {
		if m.log != nil {
			m.log.Warn("repointel: save call graph failed", zap.String("repo", id), zap.Error(err))
		}
		return
	}
	_ = m.registry.SetCallGraphFile(id, cgRel)

	// Also export an interactive HTML visualization.
	htmlRel := base + "-callgraph.html"
	htmlAbs := filepath.Join(m.cfg.MemoryDir, htmlRel)
	if err := ExportGraphHTML(htmlAbs, cg); err != nil {
		if m.log != nil {
			m.log.Warn("repointel: save call graph HTML failed", zap.String("repo", id), zap.Error(err))
		}
	}

	if m.log != nil {
		m.log.Info("repointel: call graph built",
			zap.String("repo", id),
			zap.Int("nodes", len(cg.Nodes)),
			zap.Int("edges", len(cg.Edges)),
		)
	}
}

// indexHybrid chunks memory + scan into FTS5 + vec0 for hybrid search.
// Silently skips when hybrid store is not configured.
func (m *Manager) indexHybrid(ctx context.Context, id string, mem *RepoMemory, scan *ScanResult) {
	if m.hybrid == nil {
		return
	}

	// Build all chunks.
	chunks := ChunksFromMemory(mem)
	chunks = append(chunks, ChunksFromScan(id, scan)...)
	if len(chunks) == 0 {
		return
	}

	// Embed all chunks in one batch if embedder available.
	var vecs [][]float32
	if m.cfg.Embedder != nil {
		texts := make([]string, len(chunks))
		for i, c := range chunks {
			texts[i] = c.Content
		}
		resp, err := m.cfg.Embedder.Embed(ctx, &embeddings.EmbedRequest{
			Model:     m.cfg.Embedder.DefaultModel(),
			Texts:     texts,
			InputType: embeddings.InputTypeDocument,
		})
		if err != nil {
			if m.log != nil {
				m.log.Warn("repointel: batch embed failed; FTS-only indexing", zap.String("repo", id), zap.Error(err))
			}
		} else {
			vecs = resp.Embeddings
		}
	}

	if err := m.hybrid.UpsertChunks(chunks, vecs); err != nil {
		if m.log != nil {
			m.log.Warn("repointel: hybrid store upsert failed", zap.String("repo", id), zap.Error(err))
		}
		return
	}
	if m.log != nil {
		m.log.Info("repointel: hybrid index updated",
			zap.String("repo", id),
			zap.Int("chunks", len(chunks)),
			zap.Bool("vectors", len(vecs) > 0),
		)
	}
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
