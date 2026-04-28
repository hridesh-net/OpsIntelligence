package repointel

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// VectorStore persists repo memory chunks in sqlite-vec for semantic search.
// Each repo gets up to three document chunks: architecture overview, scan
// summary, and review hints. All are stored as a single merged document under
// the repo's canonical ID.
type VectorStore struct {
	mu   sync.Mutex
	db   *sql.DB
	dims int
}

// VectorSearchResult is one result from a semantic similarity query.
type VectorSearchResult struct {
	RepoID   string
	Content  string
	Distance float64
}

// newVectorStore opens (or creates) the repointel vector database.
// dims must match the embedding model's output dimension (e.g., 1536).
// Returns (nil, nil) if dims <= 0 — callers must treat nil as disabled.
func newVectorStore(dbPath string, dims int) (*VectorStore, error) {
	if dims <= 0 {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("repointel vectorstore mkdirall: %w", err)
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("repointel vectorstore open: %w", err)
	}
	vs := &VectorStore{db: db, dims: dims}
	if err := vs.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("repointel vectorstore migrate: %w", err)
	}
	return vs, nil
}

func (vs *VectorStore) migrate() error {
	_, err := vs.db.Exec(`
		CREATE TABLE IF NOT EXISTS repo_memory_docs (
			repo_id     TEXT PRIMARY KEY,
			content     TEXT NOT NULL,
			updated_at  DATETIME NOT NULL DEFAULT (datetime('now'))
		);
	`)
	if err != nil {
		return err
	}
	// Create vec0 virtual table with fixed dimension.
	// If dims changed since last open, this is a no-op (table already exists).
	_, err = vs.db.Exec(fmt.Sprintf(`
		CREATE VIRTUAL TABLE IF NOT EXISTS vec_repo_memory USING vec0(
			repo_id TEXT PRIMARY KEY,
			embedding FLOAT[%d]
		);
	`, vs.dims))
	return err
}

// Upsert stores the content and embedding for a repo. Replaces existing entry.
func (vs *VectorStore) Upsert(repoID, content string, embedding []float32) error {
	if vs == nil {
		return nil
	}
	if len(embedding) != vs.dims {
		return fmt.Errorf("repointel vectorstore: embedding dim %d != expected %d", len(embedding), vs.dims)
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()

	tx, err := vs.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.Exec(`
		INSERT INTO repo_memory_docs (repo_id, content, updated_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(repo_id) DO UPDATE SET content=excluded.content, updated_at=excluded.updated_at
	`, repoID, content)
	if err != nil {
		return fmt.Errorf("upsert doc: %w", err)
	}

	blob := float32SliceToBytes(embedding)
	_, err = tx.Exec(`
		INSERT INTO vec_repo_memory (repo_id, embedding)
		VALUES (?, ?)
		ON CONFLICT(repo_id) DO UPDATE SET embedding=excluded.embedding
	`, repoID, blob)
	if err != nil {
		return fmt.Errorf("upsert vec: %w", err)
	}

	return tx.Commit()
}

// Delete removes a repo's entry from the vector store.
func (vs *VectorStore) Delete(repoID string) error {
	if vs == nil {
		return nil
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	tx, err := vs.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`DELETE FROM repo_memory_docs WHERE repo_id = ?`, repoID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM vec_repo_memory WHERE repo_id = ?`, repoID); err != nil {
		return err
	}
	return tx.Commit()
}

// Search returns the k nearest repos to queryEmbedding.
func (vs *VectorStore) Search(queryEmbedding []float32, k int) ([]VectorSearchResult, error) {
	if vs == nil {
		return nil, nil
	}
	if len(queryEmbedding) != vs.dims {
		return nil, fmt.Errorf("repointel vectorstore: query dim %d != expected %d", len(queryEmbedding), vs.dims)
	}
	if k <= 0 {
		k = 5
	}
	blob := float32SliceToBytes(queryEmbedding)
	vs.mu.Lock()
	defer vs.mu.Unlock()

	rows, err := vs.db.Query(`
		SELECT v.repo_id, d.content, v.distance
		FROM vec_repo_memory v
		JOIN repo_memory_docs d ON d.repo_id = v.repo_id
		WHERE v.embedding MATCH ? AND k = ?
		ORDER BY v.distance
	`, blob, k)
	if err != nil {
		return nil, fmt.Errorf("vec search: %w", err)
	}
	defer rows.Close()

	var results []VectorSearchResult
	for rows.Next() {
		var r VectorSearchResult
		if err := rows.Scan(&r.RepoID, &r.Content, &r.Distance); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// Close releases the database handle.
func (vs *VectorStore) Close() error {
	if vs == nil {
		return nil
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.db.Close()
}

// ── float32 ↔ []byte helpers ──────────────────────────────────────────────────

func float32SliceToBytes(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}
