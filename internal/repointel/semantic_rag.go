package repointel

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/embeddings"
	"github.com/opsintelligence/opsintelligence/internal/memory"
	"go.uber.org/zap"
)

// ragSourcePrefix returns the path prefix for all semantic-memory sources for repoID.
func ragSourcePrefix(repoID string) string {
	return "repo_intel/" + sanitiseID(repoID) + "/"
}

func ragSourceForChunk(base string, c Chunk) string {
	prefix := "repo_intel/" + base + "/"
	switch c.Kind {
	case ChunkFile, ChunkSource:
		safe := strings.NewReplacer("/", "∕", "\\", "∖").Replace(strings.TrimSpace(c.FilePath))
		if safe == "" {
			safe = "unknown"
		}
		if c.Kind == ChunkSource {
			return prefix + "source/" + safe
		}
		return prefix + "file/" + safe
	default:
		return prefix + string(c.Kind)
	}
}

func (m *Manager) purgeSemanticRAG(ctx context.Context, repoID string) {
	if m.cfg.SemanticRAG == nil {
		return
	}
	prefix := ragSourcePrefix(repoID)
	sources, err := m.cfg.SemanticRAG.ListSources(ctx)
	if err != nil {
		if m.log != nil {
			m.log.Warn("repointel: list semantic sources for RAG purge failed",
				zap.String("repo", repoID), zap.Error(err))
		}
		return
	}
	for _, s := range sources {
		if strings.HasPrefix(s, prefix) {
			if err := m.cfg.SemanticRAG.DeleteBySource(ctx, s); err != nil && m.log != nil {
				m.log.Warn("repointel: semantic RAG delete source",
					zap.String("repo", repoID), zap.String("source", s), zap.Error(err))
			}
		}
	}
}

// indexSemanticRAG mirrors Repo Intel chunks into the agent semantic memory DB
// used by the runner's RAG (SearchWithModel). Requires Embedder + SemanticRAG.
// fullIndexFiles is the optional full-tree snapshot (same as hybrid "source" chunks).
func (m *Manager) indexSemanticRAG(ctx context.Context, repoID string, mem *RepoMemory, scan *ScanResult, fullIndexFiles []RawFile) {
	if m.cfg.SemanticRAG == nil || m.cfg.Embedder == nil {
		return
	}

	base := sanitiseID(repoID)
	m.purgeSemanticRAG(ctx, repoID)

	chunks := ChunksFromMemory(mem)
	chunks = append(chunks, ChunksFromScan(repoID, scan)...)
	if m.indexer != nil && len(fullIndexFiles) > 0 {
		chunks = append(chunks, ChunksFromSourceFiles(repoID, fullIndexFiles, m.indexer.FullIndexChunkRunes())...)
	}
	if len(chunks) == 0 {
		return
	}

	chunkSz := m.cfg.RAGChunkSize
	if chunkSz <= 0 {
		chunkSz = 512
	}
	overlap := m.cfg.RAGChunkOverlap
	if overlap < 0 {
		overlap = 64
	}

	for _, c := range chunks {
		source := ragSourceForChunk(base, c)
		body := strings.TrimSpace(c.Heading + "\n\n" + c.Content)
		if body == "" {
			continue
		}
		fullHash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
		mdChunks := memory.ChunkMarkdown(body, chunkSz, overlap)
		if len(mdChunks) == 0 {
			continue
		}
		texts := make([]string, len(mdChunks))
		for i := range mdChunks {
			texts[i] = mdChunks[i].Content
		}
		resp, err := m.cfg.Embedder.Embed(ctx, &embeddings.EmbedRequest{
			Model:     m.cfg.Embedder.DefaultModel(),
			Texts:     texts,
			InputType: embeddings.InputTypeDocument,
		})
		if err != nil {
			if m.log != nil {
				m.log.Warn("repointel: semantic RAG embed failed",
					zap.String("repo", repoID), zap.String("source", source), zap.Error(err))
			}
			continue
		}
		if len(resp.Embeddings) != len(mdChunks) {
			if m.log != nil {
				m.log.Warn("repointel: semantic RAG embed count mismatch",
					zap.String("repo", repoID), zap.String("source", source),
					zap.Int("chunks", len(mdChunks)), zap.Int("vec", len(resp.Embeddings)))
			}
			continue
		}
		now := time.Now()
		for i, mc := range mdChunks {
			doc := memory.Document{
				ID:         fmt.Sprintf("%s#L%d-%d", source, mc.StartLine, mc.EndLine),
				Source:     source,
				Content:    mc.Content,
				Hash:       fullHash,
				SourceType: "repo_intel",
				Tags:       []string{"repo_intel", repoID},
				Model:      resp.Model,
				Embedding:  resp.Embeddings[i],
				CreatedAt:  now,
			}
			if err := m.cfg.SemanticRAG.Index(ctx, doc); err != nil && m.log != nil {
				m.log.Warn("repointel: semantic RAG index chunk failed",
					zap.String("repo", repoID), zap.String("id", doc.ID), zap.Error(err))
			}
		}
	}

	if m.log != nil {
		m.log.Info("repointel: mirrored repo intel to agent semantic RAG",
			zap.String("repo", repoID),
			zap.Int("logical_chunks", len(chunks)),
		)
	}
}
