package repointel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// RepoSymbols is a persisted index of symbols extracted during graph build.
// It mirrors CallGraph nodes (functions, methods, classes, and optional
// module nodes) for API consumers that want a flat list without edges.
type RepoSymbols struct {
	RepoID    string     `json:"repo_id"`
	UpdatedAt time.Time  `json:"updated_at"`
	Symbols   []CallNode `json:"symbols"`
}

// NewRepoSymbols builds a symbol index snapshot from a call graph.
func NewRepoSymbols(cg *CallGraph) *RepoSymbols {
	if cg == nil {
		return nil
	}
	syms := make([]CallNode, len(cg.Nodes))
	copy(syms, cg.Nodes)
	return &RepoSymbols{
		RepoID:    cg.RepoID,
		UpdatedAt: cg.CreatedAt,
		Symbols:   syms,
	}
}

// SaveRepoSymbols writes sym to path as JSON.
func SaveRepoSymbols(path string, sym *RepoSymbols) error {
	if sym == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sym, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// LoadRepoSymbols reads a RepoSymbols from path.
func LoadRepoSymbols(path string) (*RepoSymbols, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s RepoSymbols
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
