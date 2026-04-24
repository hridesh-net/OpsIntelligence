package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileTraceStore writes one JSON file per completed trace to a directory.
// Files are named "<started_at_unix>_<run_id>.json" so ls -t gives time order.
type FileTraceStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileTraceStore creates (or re-opens) a trace store in dir.
// dir is created with 0o755 permissions if it does not exist.
func NewFileTraceStore(dir string) (*FileTraceStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("trace store: mkdir %s: %w", dir, err)
	}
	return &FileTraceStore{dir: dir}, nil
}

func (s *FileTraceStore) Save(_ context.Context, t *PipelineTrace) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := fmt.Sprintf("%d_%s.json", t.StartedAt.Unix(), sanitiseID(t.RunID))
	path := filepath.Join(s.dir, name)

	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("trace store: marshal %s: %w", t.RunID, err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("trace store: write %s: %w", path, err)
	}
	return nil
}

func (s *FileTraceStore) List(_ context.Context, limit int) ([]*PipelineTrace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("trace store: readdir %s: %w", s.dir, err)
	}

	// Filter to .json files and sort newest-first by filename (unix prefix).
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	if limit > 0 && len(names) > limit {
		names = names[:limit]
	}

	out := make([]*PipelineTrace, 0, len(names))
	for _, name := range names {
		t, err := s.readFile(filepath.Join(s.dir, name))
		if err != nil {
			continue // skip corrupt files
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *FileTraceStore) Get(_ context.Context, runID string) (*PipelineTrace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("trace store: readdir: %w", err)
	}
	safe := sanitiseID(runID)
	for _, e := range entries {
		if strings.Contains(e.Name(), safe) && strings.HasSuffix(e.Name(), ".json") {
			return s.readFile(filepath.Join(s.dir, e.Name()))
		}
	}
	return nil, fmt.Errorf("trace store: run %q not found", runID)
}

func (s *FileTraceStore) readFile(path string) (*PipelineTrace, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t PipelineTrace
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// sanitiseID strips characters unsafe for filenames.
func sanitiseID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NopTraceStore discards all traces (useful in tests or when tracing is disabled).
type NopTraceStore struct{}

func (NopTraceStore) Save(_ context.Context, _ *PipelineTrace) error          { return nil }
func (NopTraceStore) List(_ context.Context, _ int) ([]*PipelineTrace, error)  { return nil, nil }
func (NopTraceStore) Get(_ context.Context, _ string) (*PipelineTrace, error) {
	return nil, fmt.Errorf("nop store: no traces")
}

// ── Run ID generator ─────────────────────────────────────────────────────────

// newRunID generates a short unique run identifier: "pr-<unixmilli>".
func newRunID() string {
	return fmt.Sprintf("pr-%d", time.Now().UnixMilli())
}
