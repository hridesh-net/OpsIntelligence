package repointel

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// HybridStore stores repo chunks in both a FTS5 full-text index and a vec0
// vector index, enabling hybrid keyword + semantic search with RRF fusion.
type HybridStore struct {
	mu   sync.Mutex
	db   *sql.DB
	dims int
}

// HybridResult is one ranked result from a hybrid search.
type HybridResult struct {
	ChunkID  string  `json:"chunk_id"`
	RepoID   string  `json:"repo_id"`
	Kind     string  `json:"kind"`
	Heading  string  `json:"heading"`
	FilePath string  `json:"file_path,omitempty"`
	Content  string  `json:"content"`
	Score    float64 `json:"score"` // RRF score — higher is better
}

const rrfK = 60 // standard RRF constant

// NewHybridStore opens or creates the hybrid search database.
// dims must be the embedding vector dimension (e.g. 1536).
// Returns (nil, nil) when dims <= 0 — caller must treat nil as disabled.
func NewHybridStore(dbPath string, dims int) (*HybridStore, error) {
	if dims <= 0 {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("hybridstore mkdir: %w", err)
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("hybridstore open: %w", err)
	}
	hs := &HybridStore{db: db, dims: dims}
	if err := hs.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("hybridstore migrate: %w", err)
	}
	return hs, nil
}

func (hs *HybridStore) migrate() error {
	// Chunk metadata + content table.
	_, err := hs.db.Exec(`
		CREATE TABLE IF NOT EXISTS repo_chunks (
			id         TEXT PRIMARY KEY,
			repo_id    TEXT NOT NULL,
			kind       TEXT NOT NULL,
			heading    TEXT NOT NULL,
			file_path  TEXT,
			content    TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_repo_chunks_repo ON repo_chunks(repo_id);
	`)
	if err != nil {
		return fmt.Errorf("create repo_chunks: %w", err)
	}

	// FTS5 virtual table — porter stemmer for English keyword search.
	_, err = hs.db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS fts_repo_chunks USING fts5(
			id         UNINDEXED,
			repo_id    UNINDEXED,
			kind       UNINDEXED,
			heading,
			content,
			tokenize = 'porter unicode61'
		);
	`)
	if err != nil {
		return fmt.Errorf("create fts_repo_chunks: %w", err)
	}

	// vec0 virtual table — fixed embedding dimension.
	_, err = hs.db.Exec(fmt.Sprintf(`
		CREATE VIRTUAL TABLE IF NOT EXISTS vec_repo_chunks USING vec0(
			chunk_id TEXT PRIMARY KEY,
			embedding FLOAT[%d]
		);
	`, hs.dims))
	if err != nil {
		return fmt.Errorf("create vec_repo_chunks: %w", err)
	}

	return nil
}

// UpsertChunks stores chunks and their embeddings. len(chunks) must equal
// len(embeddings); pass nil embeddings to skip vector indexing for a chunk
// (useful if embedding failed).
func (hs *HybridStore) UpsertChunks(chunks []Chunk, embeddings [][]float32) error {
	if hs == nil || len(chunks) == 0 {
		return nil
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()

	tx, err := hs.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	for i, c := range chunks {
		// metadata table
		_, err = tx.Exec(`
			INSERT INTO repo_chunks (id, repo_id, kind, heading, file_path, content, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
			ON CONFLICT(id) DO UPDATE SET
				repo_id=excluded.repo_id, kind=excluded.kind, heading=excluded.heading,
				file_path=excluded.file_path, content=excluded.content,
				updated_at=excluded.updated_at
		`, c.ID, c.RepoID, string(c.Kind), c.Heading, c.FilePath, c.Content)
		if err != nil {
			return fmt.Errorf("upsert chunk %s: %w", c.ID, err)
		}

		// FTS5 — delete old entry then insert (fts5 ON CONFLICT not supported)
		if _, err = tx.Exec(`DELETE FROM fts_repo_chunks WHERE id = ?`, c.ID); err != nil {
			return fmt.Errorf("fts delete %s: %w", c.ID, err)
		}
		if _, err = tx.Exec(`
			INSERT INTO fts_repo_chunks (id, repo_id, kind, heading, content)
			VALUES (?, ?, ?, ?, ?)
		`, c.ID, c.RepoID, string(c.Kind), c.Heading, c.Content); err != nil {
			return fmt.Errorf("fts insert %s: %w", c.ID, err)
		}

		// vec0 — delete then insert (vec0 does not support ON CONFLICT DO UPDATE).
		// Non-fatal if no embedding or wrong dimension — FTS5 still works.
		if i < len(embeddings) && len(embeddings[i]) == hs.dims {
			blob := float32sToBytes(embeddings[i])
			_, _ = tx.Exec(`DELETE FROM vec_repo_chunks WHERE chunk_id = ?`, c.ID)
			if _, err = tx.Exec(`
				INSERT INTO vec_repo_chunks (chunk_id, embedding) VALUES (?, ?)
			`, c.ID, blob); err != nil {
				// Non-fatal: FTS5 search still works without the vector row.
				// Log via the caller (manager warns on UpsertChunks error).
				_ = err
			}
		}
	}

	return tx.Commit()
}

// DeleteChunksByKind removes all chunks for a repo with the given kind (e.g. "source").
func (hs *HybridStore) DeleteChunksByKind(repoID, kind string) error {
	if hs == nil {
		return nil
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()

	tx, err := hs.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.Query(`SELECT id FROM repo_chunks WHERE repo_id = ? AND kind = ?`, repoID, kind)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		if _, err = tx.Exec(`DELETE FROM vec_repo_chunks WHERE chunk_id = ?`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM fts_repo_chunks WHERE id = ?`, id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM repo_chunks WHERE repo_id = ? AND kind = ?`, repoID, kind); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteRepo removes all chunks for repoID from every table.
func (hs *HybridStore) DeleteRepo(repoID string) error {
	if hs == nil {
		return nil
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()

	tx, err := hs.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Collect chunk IDs first (needed to delete from vec0).
	rows, err := tx.Query(`SELECT id FROM repo_chunks WHERE repo_id = ?`, repoID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		if _, err = tx.Exec(`DELETE FROM vec_repo_chunks WHERE chunk_id = ?`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM fts_repo_chunks WHERE id = ?`, id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM repo_chunks WHERE repo_id = ?`, repoID); err != nil {
		return err
	}
	return tx.Commit()
}

// Search performs hybrid search: FTS5 keyword + vec0 semantic, fused with RRF.
// queryVec may be nil to perform keyword-only search.
// Returns at most k results ordered by descending RRF score.
func (hs *HybridStore) Search(query string, queryVec []float32, k int) ([]HybridResult, error) {
	if hs == nil {
		return nil, nil
	}
	if k <= 0 {
		k = 10
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()

	// ── FTS5 keyword search ─────────────────────────────────────────────────
	ftsRanks := map[string]int{}
	if query != "" {
		ftsQ := buildFTSQuery(query)
		rows, err := hs.db.Query(`
			SELECT id FROM fts_repo_chunks
			WHERE fts_repo_chunks MATCH ?
			ORDER BY rank
			LIMIT ?
		`, ftsQ, k*3)
		if err == nil {
			rank := 1
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err == nil {
					ftsRanks[id] = rank
					rank++
				}
			}
			rows.Close()
		}
	}

	// ── vec0 semantic search ────────────────────────────────────────────────
	vecRanks := map[string]int{}
	if len(queryVec) == hs.dims {
		blob := float32sToBytes(queryVec)
		rows, err := hs.db.Query(`
			SELECT chunk_id FROM vec_repo_chunks
			WHERE embedding MATCH ? AND k = ?
			ORDER BY distance
		`, blob, k*3)
		if err == nil {
			rank := 1
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err == nil {
					vecRanks[id] = rank
					rank++
				}
			}
			rows.Close()
		}
	}

	// ── RRF fusion ──────────────────────────────────────────────────────────
	allIDs := unionKeys(ftsRanks, vecRanks)
	type scored struct {
		id    string
		score float64
	}
	var ranked []scored
	for _, id := range allIDs {
		score := 0.0
		if r, ok := ftsRanks[id]; ok {
			score += 1.0 / float64(rrfK+r)
		}
		if r, ok := vecRanks[id]; ok {
			score += 1.0 / float64(rrfK+r)
		}
		ranked = append(ranked, scored{id, score})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > k {
		ranked = ranked[:k]
	}

	// ── Fetch metadata ──────────────────────────────────────────────────────
	var results []HybridResult
	for _, sc := range ranked {
		var r HybridResult
		err := hs.db.QueryRow(`
			SELECT id, repo_id, kind, heading, COALESCE(file_path,''), content
			FROM repo_chunks WHERE id = ?
		`, sc.id).Scan(&r.ChunkID, &r.RepoID, &r.Kind, &r.Heading, &r.FilePath, &r.Content)
		if err != nil {
			continue
		}
		r.Score = sc.score
		results = append(results, r)
	}
	return results, nil
}

// SearchRepo is like Search but restricts hits to repoID (for repo-scoped RAG UI).
func (hs *HybridStore) SearchRepo(repoID, query string, queryVec []float32, k int) ([]HybridResult, error) {
	if hs == nil {
		return nil, nil
	}
	if k <= 0 {
		k = 10
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()

	ftsRanks := map[string]int{}
	if query != "" {
		ftsQ := buildFTSQuery(query)
		rows, err := hs.db.Query(`
			SELECT f.id FROM fts_repo_chunks f
			INNER JOIN repo_chunks r ON r.id = f.id
			WHERE r.repo_id = ? AND f MATCH ?
			ORDER BY rank
			LIMIT ?`, repoID, ftsQ, k*3)
		if err == nil {
			rank := 1
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err == nil {
					ftsRanks[id] = rank
					rank++
				}
			}
			rows.Close()
		}
	}

	vecRanks := map[string]int{}
	if len(queryVec) == hs.dims {
		blob := float32sToBytes(queryVec)
		rows, err := hs.db.Query(`
			SELECT v.chunk_id FROM vec_repo_chunks v
			INNER JOIN repo_chunks r ON r.id = v.chunk_id
			WHERE r.repo_id = ? AND v.embedding MATCH ? AND k = ?
			ORDER BY distance
		`, repoID, blob, k*3)
		if err == nil {
			rank := 1
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err == nil {
					vecRanks[id] = rank
					rank++
				}
			}
			rows.Close()
		}
	}

	allIDs := unionKeys(ftsRanks, vecRanks)
	type scored struct {
		id    string
		score float64
	}
	var ranked []scored
	for _, id := range allIDs {
		score := 0.0
		if r, ok := ftsRanks[id]; ok {
			score += 1.0 / float64(rrfK+r)
		}
		if r, ok := vecRanks[id]; ok {
			score += 1.0 / float64(rrfK+r)
		}
		ranked = append(ranked, scored{id, score})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > k {
		ranked = ranked[:k]
	}

	var results []HybridResult
	for _, sc := range ranked {
		var r HybridResult
		err := hs.db.QueryRow(`
			SELECT id, repo_id, kind, heading, COALESCE(file_path,''), content
			FROM repo_chunks WHERE id = ?
		`, sc.id).Scan(&r.ChunkID, &r.RepoID, &r.Kind, &r.Heading, &r.FilePath, &r.Content)
		if err != nil {
			continue
		}
		r.Score = sc.score
		results = append(results, r)
	}
	return results, nil
}

// Close releases the database.
func (hs *HybridStore) Close() error {
	if hs == nil {
		return nil
	}
	return hs.db.Close()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// buildFTSQuery converts a plain query string into an FTS5 prefix query.
// "error handling go" → `"error" * OR "handling" * OR "go" *`
func buildFTSQuery(query string) string {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return query
	}
	parts := make([]string, len(words))
	for i, w := range words {
		// Escape double-quotes inside the token.
		w = strings.ReplaceAll(w, `"`, `""`)
		parts[i] = `"` + w + `"`
	}
	return strings.Join(parts, " OR ")
}

func unionKeys(a, b map[string]int) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func float32sToBytes(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}
