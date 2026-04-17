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

// Service provides a shared read/write API over opsintelligence.yaml.
// It is intentionally transport-agnostic so both CLI and HTTP handlers
// can call the same mutation logic.
type Service struct {
	path string
	mu   sync.Mutex
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

func (s *Service) Read(_ context.Context) (*Snapshot, error) {
	cfg, err := config.Load(s.path)
	if err != nil {
		return nil, err
	}
	rev, err := fileRevision(s.path)
	if err != nil {
		return nil, err
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
	return fileRevision(s.path)
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
