package repointel

import (
	"fmt"
	"strings"
)

// ── Chunk ─────────────────────────────────────────────────────────────────────

// ChunkKind classifies what part of the repo a chunk represents.
type ChunkKind string

const (
	ChunkArchitecture ChunkKind = "architecture"
	ChunkFile         ChunkKind = "file"
	ChunkConventions  ChunkKind = "conventions"
	ChunkDependencies ChunkKind = "dependencies"
	ChunkReview       ChunkKind = "review"
	ChunkScan         ChunkKind = "scan"
	ChunkUserContext  ChunkKind = "user_context"
)

// Chunk is one indexable unit of repo knowledge — stored in both FTS5 and
// the vector table.
type Chunk struct {
	// ID is globally unique: "{repoID}::{kind}::{n}"
	ID       string
	RepoID   string
	Kind     ChunkKind
	Heading  string // display title shown in search results
	FilePath string // non-empty for ChunkFile
	Content  string // text content that gets embedded / FTS-indexed
}

// ── Chunkers ──────────────────────────────────────────────────────────────────

// ChunksFromMemory produces structured chunks from a fully-indexed RepoMemory.
// File chunks come from mem.RawFiles (populated by Indexer, not persisted).
func ChunksFromMemory(mem *RepoMemory) []Chunk {
	if mem == nil {
		return nil
	}
	var out []Chunk
	n := 0
	id := func(kind ChunkKind) string {
		n++
		return fmt.Sprintf("%s::%s::%d", mem.RepoID, kind, n)
	}

	// Architecture
	if mem.Architecture != "" {
		out = append(out, Chunk{
			ID:      id(ChunkArchitecture),
			RepoID:  mem.RepoID,
			Kind:    ChunkArchitecture,
			Heading: "Architecture — " + mem.RepoID,
			Content: "Architecture: " + mem.Architecture + "\nLanguage: " + mem.PrimaryLang,
		})
	}

	// Conventions
	if len(mem.Conventions) > 0 {
		var sb strings.Builder
		sb.WriteString("Coding conventions:\n")
		for _, c := range mem.Conventions {
			sb.WriteString("- " + c.Name + ": " + c.Pattern + "\n")
		}
		if mem.TestPatterns != "" {
			sb.WriteString("\nTest patterns: " + mem.TestPatterns)
		}
		if mem.CISummary != "" {
			sb.WriteString("\nCI/CD: " + mem.CISummary)
		}
		out = append(out, Chunk{
			ID:      id(ChunkConventions),
			RepoID:  mem.RepoID,
			Kind:    ChunkConventions,
			Heading: "Conventions — " + mem.RepoID,
			Content: sb.String(),
		})
	}

	// Dependencies
	if len(mem.Dependencies) > 0 {
		var sb strings.Builder
		sb.WriteString("Dependencies:\n")
		for _, d := range mem.Dependencies {
			line := "- " + d.Name
			if d.Version != "" {
				line += " @" + d.Version
			}
			if d.Purpose != "" {
				line += ": " + d.Purpose
			}
			sb.WriteString(line + "\n")
		}
		out = append(out, Chunk{
			ID:      id(ChunkDependencies),
			RepoID:  mem.RepoID,
			Kind:    ChunkDependencies,
			Heading: "Dependencies — " + mem.RepoID,
			Content: sb.String(),
		})
	}

	// Review hints + common issues
	if mem.ReviewHints != "" || len(mem.CommonIssues) > 0 {
		var sb strings.Builder
		if mem.ReviewHints != "" {
			sb.WriteString("Review focus: " + mem.ReviewHints + "\n")
		}
		if len(mem.CommonIssues) > 0 {
			sb.WriteString("\nCommon issue patterns:\n")
			for _, i := range mem.CommonIssues {
				sb.WriteString("- " + i + "\n")
			}
		}
		out = append(out, Chunk{
			ID:      id(ChunkReview),
			RepoID:  mem.RepoID,
			Kind:    ChunkReview,
			Heading: "Review Focus — " + mem.RepoID,
			Content: sb.String(),
		})
	}

	// User context
	if mem.UserContext != "" {
		out = append(out, Chunk{
			ID:      id(ChunkUserContext),
			RepoID:  mem.RepoID,
			Kind:    ChunkUserContext,
			Heading: "Operator Notes — " + mem.RepoID,
			Content: "Operator notes: " + mem.UserContext,
		})
	}

	// Raw file chunks
	for _, rf := range mem.RawFiles {
		if rf.Content == "" {
			continue
		}
		out = append(out, Chunk{
			ID:       id(ChunkFile),
			RepoID:   mem.RepoID,
			Kind:     ChunkFile,
			Heading:  rf.Path + " — " + mem.RepoID,
			FilePath: rf.Path,
			Content:  rf.Content,
		})
	}

	return out
}

// ChunksFromScan produces scan-finding chunks from a ScanResult.
func ChunksFromScan(repoID string, scan *ScanResult) []Chunk {
	if scan == nil {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("Security scan — risk: " + scan.RiskLevel + "\n")
	if scan.Summary != "" {
		sb.WriteString("Summary: " + scan.Summary + "\n")
	}
	for _, cve := range scan.CVEs {
		sb.WriteString(fmt.Sprintf("CVE [%s] %s: %s\n", cve.Severity, cve.Package, cve.Description))
	}
	for _, b := range scan.Bottlenecks {
		sb.WriteString(fmt.Sprintf("Bottleneck [%s] %s: %s\n", b.Severity, b.Location, b.Description))
	}
	for _, s := range scan.Suggestions {
		sb.WriteString(fmt.Sprintf("Suggestion [%s] %s: %s\n", s.Priority, s.Area, s.Suggestion))
	}
	return []Chunk{{
		ID:      fmt.Sprintf("%s::scan::0", repoID),
		RepoID:  repoID,
		Kind:    ChunkScan,
		Heading: "Security Scan — " + repoID,
		Content: sb.String(),
	}}
}
