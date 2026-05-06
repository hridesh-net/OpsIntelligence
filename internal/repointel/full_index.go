package repointel

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// ChunkSource marks full-repository file slices (distinct from LLM snapshot ChunkFile).
const ChunkSource ChunkKind = "source"

// ChunksFromSourceFiles splits each raw file into multiple hybrid chunks (by runes).
func ChunksFromSourceFiles(repoID string, files []RawFile, maxRunes int) []Chunk {
	if maxRunes < 256 {
		maxRunes = 256
	}
	var out []Chunk
	for _, rf := range files {
		if rf.Path == "" || rf.Content == "" {
			continue
		}
		pieces := splitByRunes(rf.Content, maxRunes)
		for i, text := range pieces {
			h := fmt.Sprintf("%x", sha256.Sum256([]byte(rf.Path)))[:12]
			id := fmt.Sprintf("%s::source::%s::%d", repoID, h, i)
			out = append(out, Chunk{
				ID:       id,
				RepoID:   repoID,
				Kind:     ChunkSource,
				Heading:  fmt.Sprintf("%s — part %d/%d", rf.Path, i+1, len(pieces)),
				FilePath: rf.Path,
				Content:  text,
			})
		}
	}
	return out
}

func splitByRunes(s string, maxRunes int) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(runes); i += maxRunes {
		end := i + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

func pathInSkippedDir(path string) bool {
	lower := strings.ToLower(path)
	segs := strings.Split(lower, "/")
	for _, s := range segs {
		switch s {
		case "node_modules", "vendor", ".git", "dist", "build", "__pycache__",
			".venv", "venv", ".next", "coverage", ".turbo", ".cache", "tmp", "temp":
			return true
		}
	}
	return false
}

func isBinaryExt(path string) bool {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return false
	}
	switch strings.ToLower(path[i:]) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".bmp",
		".woff", ".woff2", ".ttf", ".eot", ".otf",
		".pdf", ".zip", ".gz", ".tgz", ".bz2", ".7z", ".rar",
		".jar", ".war", ".exe", ".dll", ".so", ".dylib", ".bin",
		".mp3", ".mp4", ".mov", ".avi", ".mkv":
		return true
	default:
		return false
	}
}

func isIndexableTextPath(path string) bool {
	if pathInSkippedDir(path) {
		return false
	}
	if isBinaryExt(path) {
		return false
	}
	if langFromPath(path) != "" {
		return true
	}
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	lowerBase := strings.ToLower(base)
	switch lowerBase {
	case "dockerfile", "makefile", "gemfile", "rakefile", "procfile", "jenkinsfile", "containerfile":
		return true
	}
	ext := ""
	if i := strings.LastIndex(path, "."); i >= 0 {
		ext = strings.ToLower(path[i:])
	}
	switch ext {
	case ".md", ".mdx", ".txt", ".rst", ".json", ".yaml", ".yml", ".toml",
		".xml", ".html", ".htm", ".css", ".scss", ".less", ".sql",
		".graphql", ".proto":
		return true
	default:
		return false
	}
}

// selectFullIndexPaths returns blob paths to fetch for full-repo RAG (bounded).
func selectFullIndexPaths(tree []ghTreeEntry, maxFiles, maxBlobBytes int) []string {
	type cand struct {
		path string
		size int
	}
	var cands []cand
	for _, e := range tree {
		if e.Type != "blob" {
			continue
		}
		if e.Size <= 0 || e.Size > maxBlobBytes {
			continue
		}
		if !isIndexableTextPath(e.Path) {
			continue
		}
		cands = append(cands, cand{e.Path, e.Size})
	}
	// Prefer smaller files first so we cover more paths under the cap.
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0; j-- {
			if cands[j-1].size > cands[j].size {
				cands[j-1], cands[j] = cands[j], cands[j-1]
			} else {
				break
			}
		}
	}
	out := make([]string, 0, maxFiles)
	for _, c := range cands {
		if len(out) >= maxFiles {
			break
		}
		out = append(out, c.path)
	}
	return out
}

// fetchBlobContent downloads one file from GitHub contents API.
func (idx *Indexer) fetchBlobContent(ctx context.Context, base, owner, name, path, ref string) (string, error) {
	segs := strings.Split(path, "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	encPath := strings.Join(segs, "/")
	rawURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s", base, owner, name, encPath, ref)
	var contentResp struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := idx.ghGet(ctx, rawURL, &contentResp); err != nil {
		return "", err
	}
	return decodeBase64Content(contentResp.Content)
}

// FetchFullIndexRawFiles walks the full repo tree and returns every indexable
// text file within the configured size/count caps.
// For clone-mode repos the local clone is walked directly; otherwise the GitHub
// API is used. The bool indicates whether the GitHub tree response was truncated
// (always false for local clones).
func (idx *Indexer) FetchFullIndexRawFiles(ctx context.Context, entry RepoEntry, ref string) ([]RawFile, bool, error) {
	if idx.cfg.FullIndexDisable {
		return nil, false, nil
	}

	if idx.shouldUseClone(entry) {
		dir := cloneDir(idx.cfg.ClonesDir, entry.ID)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			maxFiles := idx.cfg.FullIndexMaxFiles
			maxBlob := idx.cfg.FullIndexMaxFileBytes
			files, walkErr := walkFullLocalRepo(dir, maxFiles, maxBlob)
			if walkErr != nil {
				return nil, false, walkErr
			}
			if idx.log != nil {
				idx.log.Info("repointel: full index from local clone",
					zap.String("repo", entry.ID),
					zap.Int("files", len(files)),
				)
			}
			return files, false, nil
		}
	}

	base := idx.cfg.GitHubBaseURL
	treeURL := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1",
		base, entry.Owner, entry.Name, ref)
	var treeResp struct {
		Tree      []ghTreeEntry `json:"tree"`
		Truncated bool          `json:"truncated"`
	}
	if err := idx.ghGet(ctx, treeURL, &treeResp); err != nil {
		return nil, false, fmt.Errorf("fetch tree: %w", err)
	}
	if treeResp.Truncated && idx.log != nil {
		idx.log.Warn("repointel: GitHub tree truncated; full index may be incomplete",
			zap.String("repo", entry.ID))
	}
	maxFiles := idx.cfg.FullIndexMaxFiles
	if maxFiles <= 0 {
		maxFiles = 5000
	}
	maxBlob := idx.cfg.FullIndexMaxFileBytes
	if maxBlob <= 0 {
		maxBlob = 256 * 1024
	}
	paths := selectFullIndexPaths(treeResp.Tree, maxFiles, maxBlob)
	if len(paths) == 0 {
		return nil, treeResp.Truncated, nil
	}

	conc := idx.cfg.FullIndexConcurrency
	if conc <= 0 {
		conc = 8
	}

	type result struct {
		path string
		body string
		err  error
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	resCh := make(chan result, len(paths))
	worker := func() {
		defer wg.Done()
		for p := range jobs {
			body, err := idx.fetchBlobContent(ctx, base, entry.Owner, entry.Name, p, ref)
			resCh <- result{path: p, body: body, err: err}
		}
	}
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go worker()
	}
	for _, p := range paths {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	close(resCh)

	var out []RawFile
	var nErr int
	for r := range resCh {
		if r.err != nil {
			nErr++
			continue
		}
		if strings.TrimSpace(r.body) == "" {
			continue
		}
		out = append(out, RawFile{Path: r.path, Content: r.body})
	}
	if idx.log != nil {
		idx.log.Info("repointel: full index fetch complete",
			zap.String("repo", entry.ID),
			zap.Int("paths", len(paths)),
			zap.Int("loaded", len(out)),
			zap.Int("errors", nErr),
		)
	}
	return out, treeResp.Truncated, nil
}
