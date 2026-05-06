package repointel

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// cloneDir returns the local directory for the given repo's shallow clone.
func cloneDir(clonesBase, repoID string) string {
	return filepath.Join(clonesBase, sanitiseID(repoID))
}

// resolveCloneURL returns the git clone URL for entry.
// entry.CloneURL takes precedence; otherwise a URL is inferred from Platform.
// Returns "" when no URL can be determined.
func resolveCloneURL(entry RepoEntry, token string) string {
	if entry.CloneURL != "" {
		return entry.CloneURL
	}
	owner, name := entry.Owner, entry.Name
	switch strings.ToLower(strings.TrimSpace(entry.Platform)) {
	case "github":
		if token != "" {
			return fmt.Sprintf("https://%s@github.com/%s/%s.git", token, owner, name)
		}
		return fmt.Sprintf("https://github.com/%s/%s.git", owner, name)
	case "gitlab":
		return fmt.Sprintf("https://gitlab.com/%s/%s.git", owner, name)
	case "bitbucket":
		return fmt.Sprintf("https://bitbucket.org/%s/%s.git", owner, name)
	}
	return ""
}

// ensureClone clones the repo at depth=1 if the local directory is absent,
// or pulls the latest changes if it already exists.
// Returns the local directory path and the current HEAD commit SHA.
func ensureClone(ctx context.Context, entry RepoEntry, clonesBase, token string) (localDir, headSHA string, err error) {
	localDir = cloneDir(clonesBase, entry.ID)
	cloneURL := resolveCloneURL(entry, token)
	if cloneURL == "" {
		return "", "", fmt.Errorf("repointel: cannot determine clone URL for %s", entry.ID)
	}

	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	if _, statErr := os.Stat(filepath.Join(localDir, ".git")); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(localDir, 0o755); mkErr != nil {
			return "", "", fmt.Errorf("repointel: mkdir clone dir: %w", mkErr)
		}
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1", "--single-branch", cloneURL, localDir)
		cmd.Env = env
		if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
			return "", "", fmt.Errorf("repointel: git clone %s: %w: %s", entry.ID, cmdErr, strings.TrimSpace(string(out)))
		}
	} else {
		// Pull latest; non-fatal on failure (e.g. transient network, detached HEAD).
		cmd := exec.CommandContext(ctx, "git", "-C", localDir, "pull", "--ff-only", "--depth=1")
		cmd.Env = env
		_, _ = cmd.CombinedOutput()
	}

	sha, shaErr := localHeadSHA(ctx, localDir)
	return localDir, sha, shaErr
}

// localHeadSHA returns the HEAD commit SHA of a local git repository.
func localHeadSHA(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("repointel: git rev-parse HEAD in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// remoteHeadSHA fetches from origin and returns the remote HEAD SHA without
// advancing the local branch. Used by the monitor loop to detect new commits.
func remoteHeadSHA(ctx context.Context, dir string) (string, error) {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "fetch", "--depth=1", "origin")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("repointel: git fetch in %s: %w: %s", dir, err, strings.TrimSpace(string(out)))
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "FETCH_HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("repointel: git rev-parse FETCH_HEAD in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// walkLocalRepo reads the top-priority source files from a cloned directory.
// Selection uses the same tier-based priority as the GitHub API path.
// Returns the concatenated LLM content string and per-file raw slices.
func walkLocalRepo(localDir string, maxFiles, maxFileBytesForLLM int) (string, []RawFile, error) {
	if maxFileBytesForLLM <= 0 {
		maxFileBytesForLLM = 8000
	}

	var tree []ghTreeEntry
	walkErr := filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", ".next", "dist", "build",
				"__pycache__", ".venv", "venv", ".turbo", "coverage", "tmp", "temp":
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(localDir, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		tree = append(tree, ghTreeEntry{Path: rel, Type: "blob", Size: int(info.Size())})
		return nil
	})
	if walkErr != nil {
		return "", nil, fmt.Errorf("repointel: walk %s: %w", localDir, walkErr)
	}

	selected := selectKeyFiles(tree, maxFiles)

	var sb strings.Builder
	var rawFiles []RawFile
	for _, relPath := range selected {
		abs := filepath.Join(localDir, filepath.FromSlash(relPath))
		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			continue
		}
		snippet := string(data)
		if len(snippet) > maxFileBytesForLLM {
			snippet = snippet[:maxFileBytesForLLM]
		}
		rawFiles = append(rawFiles, RawFile{Path: relPath, Content: snippet})
		sb.WriteString("### File: " + relPath + "\n```\n")
		sb.WriteString(snippet)
		if len(data) > maxFileBytesForLLM {
			sb.WriteString("\n... (truncated)\n")
		}
		sb.WriteString("\n```\n\n")
	}
	return sb.String(), rawFiles, nil
}

// walkFullLocalRepo walks the entire cloned repo and returns every indexable
// text file within the size cap. Mirrors FetchFullIndexRawFiles for local clones.
func walkFullLocalRepo(localDir string, maxFiles, maxFileBytes int) ([]RawFile, error) {
	if maxFiles <= 0 {
		maxFiles = 5000
	}
	if maxFileBytes <= 0 {
		maxFileBytes = 256 * 1024
	}

	type candidate struct {
		rel  string
		size int
	}
	var cands []candidate

	walkErr := filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if pathInSkippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(localDir, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !isIndexableTextPath(rel) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() <= 0 || int(info.Size()) > maxFileBytes {
			return nil
		}
		cands = append(cands, candidate{rel, int(info.Size())})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("repointel: full walk %s: %w", localDir, walkErr)
	}

	// Smaller files first to maximise path coverage under the cap.
	sort.Slice(cands, func(i, j int) bool { return cands[i].size < cands[j].size })

	var out []RawFile
	for _, c := range cands {
		if len(out) >= maxFiles {
			break
		}
		abs := filepath.Join(localDir, filepath.FromSlash(c.rel))
		data, readErr := os.ReadFile(abs)
		if readErr != nil || len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		out = append(out, RawFile{Path: c.rel, Content: string(data)})
	}
	return out, nil
}
