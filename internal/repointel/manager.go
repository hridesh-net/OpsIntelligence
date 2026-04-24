package repointel

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

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
}

func (c *ManagerConfig) applyDefaults() {
	if c.ProgressBuf <= 0 {
		c.ProgressBuf = 64
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
	return &Manager{
		cfg:       cfg,
		registry:  reg,
		indexer:   indexer,
		scanner:   scanner,
		log:       log,
		workQueue: make(chan string, 256),
		Progress:  make(chan ProgressEvent, cfg.ProgressBuf),
	}, nil
}

// ── Lifecycle ─────────────────────────────────────────────────────────────────

// Start runs the sequential processing loop. Call in a goroutine; returns
// when ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	if m.log != nil {
		m.log.Info("repointel manager started")
	}
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-m.workQueue:
			m.process(ctx, id)
		}
	}
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

// RemoveRepo removes the repo from the registry.
func (m *Manager) RemoveRepo(id string) error {
	return m.registry.Remove(id)
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
