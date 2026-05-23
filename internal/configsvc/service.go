package configsvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"gopkg.in/yaml.v3"
)

var ErrRevisionConflict = errors.New("configsvc: config revision conflict")

// cachedSnapshot holds an in-memory copy of the config with its file revision.
type cachedSnapshot struct {
	snap      *Snapshot
	rev       string
	modTime   time.Time
	fileSize  int64
	cachedAt  time.Time
}

// Service provides a shared read/write API over opsintelligence.yaml.
// It is intentionally transport-agnostic so both CLI and HTTP handlers
// can call the same mutation logic.
type Service struct {
	path string
	mu   sync.RWMutex

	cache *cachedSnapshot
}

func New(path string) *Service {
	path = strings.TrimSpace(path)
	if path == "" {
		path = config.DefaultConfigPath()
	}
	return &Service{path: path}
}

func (s *Service) Path() string { return s.path }

type Snapshot struct {
	Config   *config.Config `json:"config"`
	Revision string         `json:"revision"`
}

// Read returns the current config, using an in-memory cache when the file
// has not changed. This eliminates redundant disk I/O and YAML parsing on
// the hot path.
func (s *Service) Read(_ context.Context) (*Snapshot, error) {
	// Fast path: check in-memory cache with read lock.
	s.mu.RLock()
	cache := s.cache
	s.mu.RUnlock()

	if cache != nil && time.Since(cache.cachedAt) < 5*time.Second {
		st, err := os.Stat(s.path)
		if err == nil && st.ModTime().Equal(cache.modTime) && st.Size() == cache.fileSize {
			return &Snapshot{Config: cache.snap.Config, Revision: cache.rev}, nil
		}
	}

	// Slow path: load from disk and refresh cache.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock.
	if s.cache != nil && time.Since(s.cache.cachedAt) < 5*time.Second {
		st, err := os.Stat(s.path)
		if err == nil && st.ModTime().Equal(s.cache.modTime) && st.Size() == s.cache.fileSize {
			return &Snapshot{Config: s.cache.snap.Config, Revision: s.cache.rev}, nil
		}
	}

	cfg, err := config.Load(s.path)
	if err != nil {
		return nil, err
	}
	rev, err := fileRevision(s.path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(s.path)
	var modTime time.Time
	var fileSize int64
	if err == nil {
		modTime = st.ModTime()
		fileSize = st.Size()
	}

	snap := &Snapshot{Config: cfg, Revision: rev}
	s.cache = &cachedSnapshot{
		snap:     snap,
		rev:      rev,
		modTime:  modTime,
		fileSize: fileSize,
		cachedAt: time.Now(),
	}
	return &Snapshot{Config: cfg, Revision: rev}, nil
}

// Update applies mutate() and saves atomically.
func (s *Service) Update(ctx context.Context, mutate func(*config.Config) error) (string, error) {
	return s.UpdateWithRevision(ctx, "", mutate)
}

// UpdateWithRevision performs optimistic concurrency control when
// expectedRevision is non-empty.
func (s *Service) UpdateWithRevision(_ context.Context, expectedRevision string, mutate func(*config.Config) error) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := config.Load(s.path)
	if err != nil {
		return "", err
	}

	if expectedRevision != "" {
		current, err := fileRevision(s.path)
		if err != nil {
			return "", err
		}
		if current != expectedRevision {
			return "", ErrRevisionConflict
		}
	}

	if err := mutate(cfg); err != nil {
		return "", err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("configsvc: marshal: %w", err)
	}
	if err := atomicWriteFile(s.path, data, 0o600); err != nil {
		return "", err
	}
	newRev, err := fileRevision(s.path)
	if err != nil {
		return "", err
	}

	// Invalidate cache so next Read picks up the new state.
	s.cache = nil
	return newRev, nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("configsvc: mkdir %s: %w", dir, err)
	}
	tmp := filepath.Join(dir, ".opsintelligence.yaml.tmp."+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("configsvc: write temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("configsvc: rename temp: %w", err)
	}
	return nil
}

func fileRevision(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("configsvc: stat %s: %w", path, err)
	}
	return fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size()), nil
}
