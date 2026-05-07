package gateway

// rag_chat_api.go — POST /api/rag-chat
//
// Retrieves relevant chunks from the repointel hybrid store (FTS5 keyword
// search, no embeddings required). Falls back to loading repo memory JSON
// files directly when the hybrid store has no indexed chunks yet.
// Injects retrieved context into the user message, then streams the LLM
// response over SSE exactly like /api/chat.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/memory"
	"github.com/opsintelligence/opsintelligence/internal/observability/correlation"
	"github.com/opsintelligence/opsintelligence/internal/repointel"
)

// ragChatRequest is the JSON body for POST /api/rag-chat.
type ragChatRequest struct {
	Message   string   `json:"message"`
	SessionID string   `json:"session_id"`
	RepoIDs   []string `json:"repo_ids,omitempty"` // empty = all indexed repos
	Limit     int      `json:"limit,omitempty"`    // max context chunks, default 8
}

// ragSource describes one retrieved knowledge chunk for SSE attribution.
type ragSource struct {
	RepoID   string `json:"repo_id"`
	FilePath string `json:"file_path,omitempty"`
	Heading  string `json:"heading,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

// SearchRepos searches the hybrid store across all repos (FTS5, no embeddings).
func (a *RepoIntelAdapter) SearchRepos(ctx context.Context, query string, k int) ([]repointel.HybridResult, error) {
	if a == nil || a.mgr == nil {
		return nil, nil
	}
	return a.mgr.SearchRepos(ctx, query, k)
}

// SearchRepo searches the hybrid store scoped to one repo (FTS5, no embeddings).
func (a *RepoIntelAdapter) SearchRepo(ctx context.Context, repoID, query string, k int) ([]repointel.HybridResult, error) {
	if a == nil || a.mgr == nil {
		return nil, nil
	}
	return a.mgr.SearchRepo(ctx, repoID, query, k)
}

// LoadRepoMemory returns a repo's LLM-extracted memory (reads from JSON on disk).
func (a *RepoIntelAdapter) LoadRepoMemory(id string) (*repointel.RepoMemory, error) {
	if a == nil || a.mgr == nil {
		return nil, nil
	}
	return a.mgr.LoadMemory(id)
}

// ListRepoIDs returns IDs of all registered repos.
func (a *RepoIntelAdapter) ListRepoIDs() []string {
	if a == nil || a.mgr == nil {
		return nil
	}
	entries := a.mgr.ListRepos()
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids
}

// handleRAGChat handles POST /api/rag-chat.
//
// Flow:
//  1. Parse {message, session_id, repo_ids?, limit?}
//  2. Search hybrid store (FTS5). Falls back to memory JSON if store is empty.
//  3. Emit SSE "sources" event with attribution list.
//  4. Inject retrieved context into user message.
//  5. Stream response via agent runner (same path as /api/chat).
func (s *Server) handleRAGChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Runner == nil {
		http.Error(w, `{"error":"agent not initialised"}`, http.StatusServiceUnavailable)
		return
	}

	var req ragChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
		return
	}
	if req.Limit <= 0 || req.Limit > 20 {
		req.Limit = 8
	}
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// ── Retrieve RAG context ─────────────────────────────────────────────────
	var ragContext string
	var sources []ragSource
	if s.RepoIntel != nil {
		ragContext, sources = s.buildRAGContext(r.Context(), req)
	}

	// ── SSE setup ────────────────────────────────────────────────────────────
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Emit sources event before streaming so UI shows attribution immediately.
	if len(sources) > 0 {
		payload, _ := json.Marshal(map[string]any{
			"type":    "sources",
			"sources": sources,
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}

	// ── Build augmented user message ─────────────────────────────────────────
	var augmented strings.Builder
	if ragContext != "" {
		augmented.WriteString(ragContext)
		augmented.WriteString("\n---\n\n**Question:** ")
	}
	augmented.WriteString(req.Message)
	if ragContext == "" {
		augmented.WriteString("\n\n*(No repository context found — answering from general knowledge.)*")
	}

	// ── Stream via runner ─────────────────────────────────────────────────────
	ctx := correlation.WithSessionID(r.Context(), sessionID)
	ctx = correlation.WithChannel(ctx, "rag-chat")

	done := make(chan struct{})
	handler := &sseStreamHandler{w: w, flusher: flusher, done: done}
	sessionRunner := s.Runner.WithSession(sessionID)

	go func() {
		sessionRunner.RunStream(ctx, memory.Message{
			ID:        uuid.New().String(),
			SessionID: sessionID,
			Role:      memory.RoleUser,
			Content:   augmented.String(),
			CreatedAt: time.Now(),
		}, handler)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// buildRAGContext searches the hybrid store and falls back to memory JSON files.
// Returns a formatted markdown context block and attribution list.
func (s *Server) buildRAGContext(ctx context.Context, req ragChatRequest) (string, []ragSource) {
	repoIDs := req.RepoIDs
	if len(repoIDs) == 0 {
		repoIDs = s.RepoIntel.ListRepoIDs()
	}
	if len(repoIDs) == 0 {
		return "", nil
	}

	// ── FTS5 hybrid search ───────────────────────────────────────────────────
	var hits []repointel.HybridResult
	seen := map[string]bool{}
	perRepo := req.Limit
	if len(repoIDs) > 1 {
		perRepo = (req.Limit/len(repoIDs) + 1)
	}
	for _, rid := range repoIDs {
		sub, err := s.RepoIntel.SearchRepo(ctx, rid, req.Message, perRepo)
		if err == nil {
			for _, h := range sub {
				if !seen[h.ChunkID] {
					seen[h.ChunkID] = true
					hits = append(hits, h)
				}
			}
		}
	}

	// ── Fallback: memory JSON ─────────────────────────────────────────────────
	if len(hits) == 0 {
		return s.buildMemoryFallbackContext(repoIDs)
	}

	// ── Format FTS5 results ───────────────────────────────────────────────────
	var sb strings.Builder
	sb.WriteString("## Repository Knowledge Base\n")
	sb.WriteString("Ground your answer in the evidence retrieved from indexed repositories below.\n\n")

	var refs []ragSource
	for i, h := range hits {
		if i >= req.Limit {
			break
		}
		sb.WriteString(fmt.Sprintf("**[%d]** `%s`", i+1, h.RepoID))
		if h.FilePath != "" {
			sb.WriteString(fmt.Sprintf(" · `%s`", h.FilePath))
		}
		if h.Heading != "" {
			sb.WriteString(fmt.Sprintf(" — %s", h.Heading))
		}
		sb.WriteString("\n")
		content := h.Content
		if len(content) > 1200 {
			content = content[:1200] + "…"
		}
		sb.WriteString("```\n" + content + "\n```\n\n")
		refs = append(refs, ragSource{
			RepoID:   h.RepoID,
			FilePath: h.FilePath,
			Heading:  h.Heading,
			Kind:     h.Kind,
		})
	}
	return sb.String(), refs
}

// buildMemoryFallbackContext loads repo memory JSON files and builds a context
// block when the hybrid store has no indexed chunks yet.
func (s *Server) buildMemoryFallbackContext(repoIDs []string) (string, []ragSource) {
	var sb strings.Builder
	var refs []ragSource
	hasContent := false

	sb.WriteString("## Repository Knowledge Base\n")
	sb.WriteString("The following information was loaded from indexed repository memory.\n\n")

	for _, rid := range repoIDs {
		mem, err := s.RepoIntel.LoadRepoMemory(rid)
		if err != nil || mem == nil {
			continue
		}
		hasContent = true
		sb.WriteString(fmt.Sprintf("### `%s`\n", rid))

		if mem.Architecture != "" {
			sb.WriteString("**Architecture:** " + mem.Architecture + "\n\n")
			refs = append(refs, ragSource{RepoID: rid, Heading: "Architecture", Kind: "architecture"})
		}
		if mem.PrimaryLang != "" {
			langs := mem.PrimaryLang
			if len(mem.Languages) > 0 {
				langs = strings.Join(mem.Languages, ", ")
			}
			sb.WriteString("**Languages:** " + langs + "\n\n")
		}
		if len(mem.Dependencies) > 0 {
			sb.WriteString("**Key Dependencies:**\n")
			n := len(mem.Dependencies)
			if n > 10 {
				n = 10
			}
			for _, d := range mem.Dependencies[:n] {
				line := "- `" + d.Name + "`"
				if d.Version != "" {
					line += " " + d.Version
				}
				if d.Purpose != "" {
					line += ": " + d.Purpose
				}
				sb.WriteString(line + "\n")
			}
			sb.WriteString("\n")
		}
		if len(mem.Conventions) > 0 {
			sb.WriteString("**Conventions:**\n")
			for _, c := range mem.Conventions {
				sb.WriteString("- **" + c.Name + ":** " + c.Pattern + "\n")
			}
			sb.WriteString("\n")
		}
		if mem.ReviewHints != "" {
			sb.WriteString("**Review Focus:** " + mem.ReviewHints + "\n\n")
		}
		if len(mem.CommonIssues) > 0 {
			sb.WriteString("**Common Issues:**\n")
			for _, issue := range mem.CommonIssues {
				sb.WriteString("- " + issue + "\n")
			}
			sb.WriteString("\n")
		}
		if mem.TestPatterns != "" {
			sb.WriteString("**Test Patterns:** " + mem.TestPatterns + "\n\n")
		}
		if mem.CISummary != "" {
			sb.WriteString("**CI/CD:** " + mem.CISummary + "\n\n")
		}
	}

	if !hasContent {
		return "", nil
	}
	return sb.String(), refs
}
