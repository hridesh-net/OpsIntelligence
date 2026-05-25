// Package worktree manages git worktree isolation for kanban card runs.
// Each run gets its own worktree so agents can operate on a clean branch
// without interfering with the working tree or other runs.
package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Manager creates, tracks, and cleans up git worktrees for card runs.
type Manager struct {
	baseDir string
	log     *zap.Logger
	mu      sync.Mutex
	// active maps runID -> worktree path
	active map[string]*Entry
}

// Entry tracks one worktree allocation.
type Entry struct {
	RunID    string
	CardID   string
	RepoURL  string
	Branch   string
	Path     string
	BaseDir  string
	CreatedAt time.Time
}

// NewManager creates a worktree manager rooted at baseDir.
// baseDir is typically <state_dir>/workspace/kanban.
func NewManager(baseDir string, log *zap.Logger) *Manager {
	if log == nil {
		log = zap.NewNop()
	}
	return &Manager{
		baseDir: baseDir,
		log:     log,
		active:  make(map[string]*Entry),
	}
}

// EnsureBase creates the base directory if it doesn't exist.
func (m *Manager) EnsureBase() error {
	return os.MkdirAll(m.baseDir, 0o755)
}

// Create prepares a git worktree for a run.
//
// If repoURL is empty, it falls back to boardLocalPath (the board's local repo).
// If boardLocalPath is also empty, it returns an error.
//
// The branch is created from baseBranch (or "main" / "master" if empty).
// The returned Entry.Path is the absolute path to the worktree checkout.
func (m *Manager) Create(ctx context.Context, runID, cardID, repoURL, boardLocalPath, branch, baseBranch string) (*Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if runID == "" {
		runID = uuid.NewString()
	}

	// Determine the source repo path.
	srcRepo, err := m.resolveSourceRepo(ctx, repoURL, boardLocalPath)
	if err != nil {
		return nil, err
	}

	// Determine the target branch name.
	if branch == "" {
		branch = fmt.Sprintf("kanban/%s-%s", cardID, runID[:8])
	}

	// Worktree directory: <baseDir>/<cardID>/<runID>
	wtPath := filepath.Join(m.baseDir, cardID, runID)
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir worktree: %w", err)
	}

	// Create the worktree.
	if err := m.gitWorktreeAdd(ctx, srcRepo, wtPath, branch, baseBranch); err != nil {
		_ = os.RemoveAll(wtPath)
		return nil, fmt.Errorf("git worktree add: %w", err)
	}

	ent := &Entry{
		RunID:     runID,
		CardID:    cardID,
		RepoURL:   repoURL,
		Branch:    branch,
		Path:      wtPath,
		BaseDir:   m.baseDir,
		CreatedAt: time.Now().UTC(),
	}
	m.active[runID] = ent

	m.log.Info("worktree created",
		zap.String("run_id", runID),
		zap.String("card_id", cardID),
		zap.String("path", wtPath),
		zap.String("branch", branch),
	)
	return ent, nil
}

// Get returns an active worktree entry by runID.
func (m *Manager) Get(runID string) (*Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, ok := m.active[runID]
	return ent, ok
}

// Remove deletes a worktree and its tracking entry.
func (m *Manager) Remove(ctx context.Context, runID string) error {
	m.mu.Lock()
	ent, ok := m.active[runID]
	if !ok {
		m.mu.Unlock()
		return nil // already gone
	}
	delete(m.active, runID)
	m.mu.Unlock()

	if err := m.gitWorktreeRemove(ctx, ent.Path); err != nil {
		m.log.Warn("git worktree remove failed, falling back to rm -rf",
			zap.String("run_id", runID),
			zap.Error(err),
		)
	}
	if err := os.RemoveAll(ent.Path); err != nil {
		m.log.Warn("rm -rf worktree failed",
			zap.String("path", ent.Path),
			zap.Error(err),
		)
	}
	// Try to clean up empty parent directories.
	m.pruneEmptyDirs(ent.CardID)

	m.log.Info("worktree removed", zap.String("run_id", runID), zap.String("path", ent.Path))
	return nil
}

// Promote merges the worktree branch into baseBranch and pushes if origin exists.
func (m *Manager) Promote(ctx context.Context, runID, baseBranch, commitMsg string) error {
	ent, ok := m.Get(runID)
	if !ok {
		return fmt.Errorf("worktree not found for run %s", runID)
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("Kanban run %s changes", runID[:8])
	}

	// Stage and commit any changes.
	if err := m.git(ctx, ent.Path, "add", "."); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	// Only commit if there are changes.
	if hasChanges, _ := m.hasDiff(ctx, ent.Path); hasChanges {
		if err := m.git(ctx, ent.Path, "commit", "-m", commitMsg); err != nil {
			return fmt.Errorf("git commit: %w", err)
		}
	}

	// Push the branch.
	if err := m.git(ctx, ent.Path, "push", "-u", "origin", ent.Branch); err != nil {
		m.log.Warn("push failed (origin may not exist)", zap.Error(err))
	}

	return nil
}

// resolveSourceRepo returns the local path to a git repo.
// If repoURL is set and boardLocalPath is empty, it clones to a cache.
func (m *Manager) resolveSourceRepo(ctx context.Context, repoURL, boardLocalPath string) (string, error) {
	if boardLocalPath != "" {
		if _, err := os.Stat(filepath.Join(boardLocalPath, ".git")); err == nil {
			return boardLocalPath, nil
		}
	}
	if repoURL == "" {
		return "", fmt.Errorf("no repo_url or board repo_path available")
	}

	// Clone to a shared cache under baseDir/_repos/<sanitized-url>.
	cacheDir := filepath.Join(m.baseDir, "_repos", sanitizeRepoDir(repoURL))
	if _, err := os.Stat(filepath.Join(cacheDir, ".git")); err == nil {
		// Already cloned — fetch latest.
		_ = m.git(ctx, cacheDir, "fetch", "--all")
		return cacheDir, nil
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	if err := m.git(ctx, cacheDir, "clone", "--bare", repoURL, "."); err != nil {
		_ = os.RemoveAll(cacheDir)
		return "", fmt.Errorf("clone %s: %w", repoURL, err)
	}
	return cacheDir, nil
}

func (m *Manager) gitWorktreeAdd(ctx context.Context, srcRepo, wtPath, branch, baseBranch string) error {
	// If the branch already exists locally, check it out.
	exists, _ := m.branchExists(ctx, srcRepo, branch)
	if exists {
		return m.git(ctx, srcRepo, "worktree", "add", wtPath, branch)
	}

	// Create a new branch from baseBranch.
	if baseBranch == "" {
		baseBranch = m.inferDefaultBranch(ctx, srcRepo)
	}
	return m.git(ctx, srcRepo, "worktree", "add", "-b", branch, wtPath, baseBranch)
}

func (m *Manager) gitWorktreeRemove(ctx context.Context, wtPath string) error {
	return m.git(ctx, wtPath, "worktree", "remove", "--force", wtPath)
}

func (m *Manager) branchExists(ctx context.Context, repo, branch string) (bool, error) {
	out, err := m.gitOutput(ctx, repo, "branch", "--list", branch)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (m *Manager) inferDefaultBranch(ctx context.Context, repo string) string {
	for _, b := range []string{"main", "master"} {
		out, err := m.gitOutput(ctx, repo, "branch", "--list", b)
		if err == nil && strings.TrimSpace(out) != "" {
			return b
		}
	}
	return "main"
}

func (m *Manager) hasDiff(ctx context.Context, repo string) (bool, error) {
	out, err := m.gitOutput(ctx, repo, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (m *Manager) git(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (m *Manager) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func (m *Manager) pruneEmptyDirs(cardID string) {
	cardDir := filepath.Join(m.baseDir, cardID)
	if empty, _ := isDirEmpty(cardDir); empty {
		_ = os.Remove(cardDir)
	}
}

func isDirEmpty(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	return len(names) == 0 && err == nil, nil
}

func sanitizeRepoDir(url string) string {
	// Turn https://github.com/acme/repo.git → github.com_acme_repo
	s := url
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "git@")
	s = strings.ReplaceAll(s, ":/", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.TrimSuffix(s, ".git")
	return s
}
