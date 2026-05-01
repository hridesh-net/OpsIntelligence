package repointel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/devops/pipeline"
	"github.com/opsintelligence/opsintelligence/internal/provider"
	"go.uber.org/zap"
)

// ── Indexer ───────────────────────────────────────────────────────────────────

// IndexerConfig holds tuning parameters for the Indexer.
type IndexerConfig struct {
	// GitHubToken is the PAT used to fetch file contents via the GitHub API.
	GitHubToken string

	// GitHubBaseURL defaults to https://api.github.com.
	GitHubBaseURL string

	// MaxFilesPerRepo caps how many files are fetched and sent to the LLM.
	// Default 20.
	MaxFilesPerRepo int

	// MemoryDir is the directory where per-repo memory JSON files are written.
	MemoryDir string

	// FullIndexDisable skips the full-tree fetch used for repo-scoped RAG (hybrid + semantic).
	FullIndexDisable bool

	// FullIndexMaxFiles caps how many text blobs are fetched per repo (default 5000).
	FullIndexMaxFiles int

	// FullIndexMaxFileBytes skips blobs larger than this (default 256 KiB).
	FullIndexMaxFileBytes int

	// FullIndexChunkRunes splits each file into hybrid chunks of roughly this many runes (default 3500).
	FullIndexChunkRunes int

	// FullIndexConcurrency bounds parallel GitHub blob downloads (default 8).
	FullIndexConcurrency int
}

func (c *IndexerConfig) applyDefaults() {
	if c.GitHubBaseURL == "" {
		c.GitHubBaseURL = "https://api.github.com"
	}
	c.GitHubBaseURL = strings.TrimRight(c.GitHubBaseURL, "/")
	if c.MaxFilesPerRepo <= 0 {
		// Slightly higher default improves graph + memory coverage on mid-size repos.
		c.MaxFilesPerRepo = 32
	}
	if c.FullIndexMaxFiles <= 0 {
		c.FullIndexMaxFiles = 5000
	}
	if c.FullIndexMaxFileBytes <= 0 {
		c.FullIndexMaxFileBytes = 256 * 1024
	}
	if c.FullIndexChunkRunes <= 0 {
		c.FullIndexChunkRunes = 3500
	}
	if c.FullIndexConcurrency <= 0 {
		c.FullIndexConcurrency = 8
	}
}

// Indexer fetches a repo's codebase via the GitHub API (with git-clone as
// fallback) and uses the LLMRouter to produce a structured RepoMemory.
type Indexer struct {
	cfg    IndexerConfig
	router *pipeline.LLMRouter
	log    *zap.Logger
	http   *http.Client
}

// NewIndexer constructs an Indexer.
func NewIndexer(cfg IndexerConfig, router *pipeline.LLMRouter, log *zap.Logger) *Indexer {
	cfg.applyDefaults()
	return &Indexer{
		cfg:    cfg,
		router: router,
		log:    log,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

// Index fetches and analyses the repo, returning a populated RepoMemory.
// The caller is responsible for persisting the result.
func (idx *Indexer) Index(ctx context.Context, entry RepoEntry) (*RepoMemory, error) {
	if idx.log != nil {
		idx.log.Info("indexing repo", zap.String("repo", entry.ID))
	}

	// ── Step 1: fetch key files ────────────────────────────────────────────────
	content, rawFiles, headSHA, err := idx.fetchRepoContent(ctx, entry)
	if err != nil {
		return nil, fmt.Errorf("indexer: fetch %s: %w", entry.ID, err)
	}

	// ── Step 2: route LLM call ────────────────────────────────────────────────
	// Treat every indexing call as high complexity (send to primary LLM for quality).
	route, err := idx.router.Route(ctx, 999, []string{"go.mod", "package.json", "Dockerfile"})
	if err != nil {
		return nil, fmt.Errorf("indexer: route LLM: %w", err)
	}

	// ── Step 3: call LLM ──────────────────────────────────────────────────────
	mem, err := idx.analyseWithLLM(ctx, route, entry, content)
	if err != nil {
		return nil, fmt.Errorf("indexer: LLM analysis: %w", err)
	}
	mem.RepoID = entry.ID
	mem.UpdatedAt = time.Now()
	mem.HeadSHA = headSHA
	mem.RawFiles = rawFiles // available to caller for chunking; not persisted to JSON

	idx.router.RecordSuccess(route.ProviderID)

	if idx.log != nil {
		idx.log.Info("indexing complete",
			zap.String("repo", entry.ID),
			zap.String("lang", mem.PrimaryLang),
			zap.Int("conventions", len(mem.Conventions)),
		)
	}
	return mem, nil
}

// FetchRawFiles returns the same per-file snapshot used during indexing (GitHub
// tree + blob fetch, truncated per file) without calling the LLM. Used to build
// or repair call graphs when RepoMemory.RawFiles is empty (e.g. memory was
// loaded from disk only).
func (idx *Indexer) FetchRawFiles(ctx context.Context, entry RepoEntry) ([]RawFile, error) {
	if idx == nil {
		return nil, fmt.Errorf("indexer: nil")
	}
	_, raw, _, err := idx.fetchRepoContent(ctx, entry)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// ── GitHub API fetch ──────────────────────────────────────────────────────────

// ghTreeEntry is a single node in a GitHub tree response.
type ghTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "blob" | "tree"
	SHA  string `json:"sha"`
	Size int    `json:"size"`
	URL  string `json:"url"`
}

// fetchRepoContent fetches key file contents via the GitHub Trees API.
// Returns concatenated content for LLM, per-file raw files for chunking, and HEAD SHA.
func (idx *Indexer) fetchRepoContent(ctx context.Context, entry RepoEntry) (string, []RawFile, string, error) {
	base := idx.cfg.GitHubBaseURL

	// 1. Resolve HEAD SHA.
	headSHA, err := idx.fetchHeadSHA(ctx, base, entry.Owner, entry.Name)
	if err != nil {
		return "", nil, "", err
	}

	// 2. Fetch the full tree.
	treeURL := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1",
		base, entry.Owner, entry.Name, headSHA)
	var treeResp struct {
		Tree      []ghTreeEntry `json:"tree"`
		Truncated bool          `json:"truncated"`
	}
	if err := idx.ghGet(ctx, treeURL, &treeResp); err != nil {
		return "", nil, "", fmt.Errorf("fetch tree: %w", err)
	}

	// 3. Select key files.
	files := selectKeyFiles(treeResp.Tree, idx.cfg.MaxFilesPerRepo)

	// 4. Fetch each file's content.
	var sb strings.Builder
	var rawFiles []RawFile
	for _, f := range files {
		rawURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
			base, entry.Owner, entry.Name, f, headSHA)
		var contentResp struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		if err := idx.ghGet(ctx, rawURL, &contentResp); err != nil {
			continue
		}
		decoded, err := decodeBase64Content(contentResp.Content)
		if err != nil {
			continue
		}
		// Cap per-file content for the LLM prompt and for chunk storage.
		const maxFileBytes = 8000
		snippet := decoded
		if len(snippet) > maxFileBytes {
			snippet = snippet[:maxFileBytes]
		}
		rawFiles = append(rawFiles, RawFile{Path: f, Content: snippet})
		sb.WriteString("### File: " + f + "\n```\n")
		sb.WriteString(snippet)
		if len(decoded) > maxFileBytes {
			sb.WriteString("\n... (truncated)\n")
		}
		sb.WriteString("\n```\n\n")
	}
	return sb.String(), rawFiles, headSHA, nil
}

// CurrentHeadSHA fetches the current HEAD commit SHA for the repo via a
// lightweight API call (no file content fetched). Used by the monitor loop.
func (idx *Indexer) CurrentHeadSHA(ctx context.Context, entry RepoEntry) (string, error) {
	return idx.fetchHeadSHA(ctx, idx.cfg.GitHubBaseURL, entry.Owner, entry.Name)
}

func (idx *Indexer) fetchHeadSHA(ctx context.Context, base, owner, name string) (string, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/commits?per_page=1", base, owner, name)
	var commits []struct {
		SHA string `json:"sha"`
	}
	if err := idx.ghGet(ctx, u, &commits); err != nil {
		return "", fmt.Errorf("fetch HEAD SHA: %w", err)
	}
	if len(commits) == 0 {
		return "", fmt.Errorf("repo has no commits")
	}
	return commits[0].SHA, nil
}

func (idx *Indexer) ghGet(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if idx.cfg.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+idx.cfg.GitHubToken)
	}
	resp, err := idx.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("github API %s: %s", resp.Status, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// isLikelySourcePath matches common implementation files so they are not
// starved out by tier-4 noise (tiny JSON/YAML) when MaxFilesPerRepo is tight.
// That starvation produced empty call graphs for Go repos while TS repos
// often won via package.json + index.ts entry-point tiering.
func isLikelySourcePath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".py", ".rs", ".java", ".cs", ".php", ".rb",
		".cpp", ".cc", ".cxx", ".c", ".h", ".hpp":
		return true
	default:
		return false
	}
}

// selectKeyFiles picks the most useful files from the tree for LLM analysis.
func selectKeyFiles(tree []ghTreeEntry, max int) []string {
	// Priority tiers: manifests > CI > (entry points + source) > docs > other.
	tier := func(path string) int {
		p := strings.ToLower(path)
		switch {
		case isManifest(p):
			return 0
		case isCI(p):
			return 1
		case isEntryPoint(p), isLikelySourcePath(path):
			return 2
		case isDoc(p):
			return 3
		default:
			return 4
		}
	}

	// Collect blobs only, skip very large files.
	type candidate struct {
		path string
		tier int
		size int
	}
	var cands []candidate
	for _, e := range tree {
		if e.Type != "blob" {
			continue
		}
		if e.Size > 200_000 { // skip files > 200 KB
			continue
		}
		cands = append(cands, candidate{e.Path, tier(e.Path), e.Size})
	}

	// Sort by tier, then:
	//   tier 2 (source): larger files first — more defs/imports per 8 KiB cap;
	//   other tiers: smaller first — token budget for manifests and misc.
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.tier == 2 {
			if a.size != b.size {
				return a.size > b.size
			}
			return a.path < b.path
		}
		if a.size != b.size {
			return a.size < b.size
		}
		return a.path < b.path
	})

	out := make([]string, 0, max)
	for _, c := range cands {
		if len(out) >= max {
			break
		}
		out = append(out, c.path)
	}
	return out
}

func isManifest(p string) bool {
	for _, m := range []string{
		"go.mod", "go.sum", "package.json", "cargo.toml", "requirements.txt",
		"pyproject.toml", "pom.xml", "build.gradle", "gemfile", "composer.json",
		"pubspec.yaml",
	} {
		if strings.HasSuffix(p, m) {
			return true
		}
	}
	return false
}

func isCI(p string) bool {
	return strings.Contains(p, ".github/workflows") ||
		strings.HasSuffix(p, ".gitlab-ci.yml") ||
		strings.HasSuffix(p, ".circleci/config.yml") ||
		strings.HasSuffix(p, "jenkinsfile") ||
		strings.HasSuffix(p, ".travis.yml")
}

func isEntryPoint(p string) bool {
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	for _, e := range []string{
		"main.go", "main.py", "index.js", "index.ts", "app.py", "server.go",
		"server.js", "app.go", "cmd/main.go", "readme.md",
	} {
		if strings.EqualFold(base, e) {
			return true
		}
	}
	return false
}

func isDoc(p string) bool {
	p = strings.ToLower(p)
	return strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".rst") ||
		strings.HasSuffix(p, ".txt") || strings.HasSuffix(p, "architecture.md")
}

func decodeBase64Content(encoded string) (string, error) {
	// GitHub returns base64 with newlines; strip them before decoding.
	cleaned := strings.ReplaceAll(encoded, "\n", "")
	b, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ── LLM analysis ──────────────────────────────────────────────────────────────

func (idx *Indexer) analyseWithLLM(
	ctx context.Context,
	route pipeline.RouteResult,
	entry RepoEntry,
	content string,
) (*RepoMemory, error) {
	prompt := buildIndexPrompt(entry, content)

	req := &provider.CompletionRequest{
		Model:        route.Model,
		MaxTokens:    2048,
		SystemPrompt: indexSystemPrompt,
		Messages: []provider.Message{
			{
				Role: provider.RoleUser,
				Content: []provider.ContentPart{
					{Type: provider.ContentTypeText, Text: prompt},
				},
			},
		},
	}

	resp, err := route.Provider.Complete(ctx, req)
	if err != nil {
		idx.router.RecordFailure(route.ProviderID)
		return nil, err
	}

	text := resp.Text()
	mem, err := parseMemoryJSON(text)
	if err != nil {
		// If JSON parsing fails, return a minimal memory with raw architecture text.
		return &RepoMemory{Architecture: truncate(text, 2000)}, nil
	}
	return mem, nil
}

const indexSystemPrompt = `You are a senior software architect analysing a codebase for an enterprise code intelligence system.

Your job: analyse the provided file excerpts and return a JSON object that matches this exact schema:

{
  "architecture": "concise high-level description (2-4 sentences)",
  "primary_lang": "e.g. Go",
  "languages": ["Go", "TypeScript"],
  "key_files": ["cmd/main.go", "internal/server/server.go"],
  "conventions": [
    {"name": "error wrapping", "pattern": "fmt.Errorf with %w verb"},
    {"name": "table-driven tests", "pattern": "[]struct{name,input,want} in _test.go files"}
  ],
  "dependencies": [
    {"name": "github.com/gin-gonic/gin", "version": "v1.9.0", "purpose": "HTTP router"},
    {"name": "go.uber.org/zap", "version": "v1.26.0", "purpose": "structured logging"}
  ],
  "test_patterns": "describe how tests are organised",
  "ci_summary": "describe the CI/CD pipeline",
  "review_hints": "what reviewers should focus on for this codebase",
  "common_issues": ["list of recurring bug patterns or risky patterns observed in the code"]
}

Return ONLY the JSON object. No markdown fences. No explanation.`

func buildIndexPrompt(entry RepoEntry, content string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Repo: %s/%s\n", entry.Owner, entry.Name))
	if entry.Description != "" {
		sb.WriteString("Description: " + entry.Description + "\n")
	}
	sb.WriteString("\nKey file excerpts:\n\n")
	sb.WriteString(content)
	sb.WriteString("\n\nAnalyse the above and return the JSON schema as instructed.")
	return sb.String()
}

// parseMemoryJSON unmarshals the LLM response into a RepoMemory.
// It tolerates JSON wrapped in markdown fences.
func parseMemoryJSON(raw string) (*RepoMemory, error) {
	s := strings.TrimSpace(raw)
	// Strip ``` fences if present.
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}

	// The LLM response uses snake_case keys that map directly to RepoMemory
	// json tags. Use an intermediate map to handle the conventions/dependencies
	// arrays which have named sub-keys.
	var raw2 struct {
		Architecture string   `json:"architecture"`
		PrimaryLang  string   `json:"primary_lang"`
		Languages    []string `json:"languages"`
		KeyFiles     []string `json:"key_files"`
		Conventions  []struct {
			Name    string `json:"name"`
			Pattern string `json:"pattern"`
		} `json:"conventions"`
		Dependencies []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Purpose string `json:"purpose"`
		} `json:"dependencies"`
		TestPatterns string   `json:"test_patterns"`
		CISummary    string   `json:"ci_summary"`
		ReviewHints  string   `json:"review_hints"`
		CommonIssues []string `json:"common_issues"`
	}
	if err := json.Unmarshal([]byte(s), &raw2); err != nil {
		return nil, err
	}
	mem := &RepoMemory{
		Architecture: raw2.Architecture,
		PrimaryLang:  raw2.PrimaryLang,
		Languages:    raw2.Languages,
		KeyFiles:     raw2.KeyFiles,
		TestPatterns: raw2.TestPatterns,
		CISummary:    raw2.CISummary,
		ReviewHints:  raw2.ReviewHints,
		CommonIssues: raw2.CommonIssues,
	}
	for _, c := range raw2.Conventions {
		mem.Conventions = append(mem.Conventions, CodeConvention{Name: c.Name, Pattern: c.Pattern})
	}
	for _, d := range raw2.Dependencies {
		mem.Dependencies = append(mem.Dependencies, Dependency{Name: d.Name, Version: d.Version, Purpose: d.Purpose})
	}
	return mem, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// FullIndexChunkRunes returns the configured rune window for full-repo source chunks.
func (idx *Indexer) FullIndexChunkRunes() int {
	if idx == nil || idx.cfg.FullIndexChunkRunes <= 0 {
		return 3500
	}
	return idx.cfg.FullIndexChunkRunes
}
