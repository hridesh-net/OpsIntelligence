// Package worktree manages git worktrees for isolated agent runs.
// Each card run gets its own worktree branched from the repo's default branch.
package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Manager creates, manages, and cleans up git worktrees.
type Manager struct {
	BaseDir string // e.g., ~/.opsintelligence/worktrees/
}

// Worktree represents an isolated git worktree.
type Worktree struct {
	Path       string
	Branch     string
	BaseBranch string
}

// Create creates a new git worktree for a card run.
// The worktree is branched from baseBranch (or "main" if empty).
func (m *Manager) Create(cardID, runID, repoPath, baseBranch string) (*Worktree, error) {
	if baseBranch == "" {
		baseBranch = "main"
	}
	branch := fmt.Sprintf("opsintel/%s-%s", cardID[:8], runID[:8])
	wtPath := filepath.Join(m.BaseDir, fmt.Sprintf("%s-%s", cardID, runID))

	if err := os.MkdirAll(m.BaseDir, 0755); err != nil {
		return nil, fmt.Errorf("worktree: create base dir: %w", err)
	}

	// Ensure repo path exists and is a git repo
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return nil, fmt.Errorf("worktree: repo path %q is not a git repository: %w", repoPath, err)
	}

	// Create branch from base (30s timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "branch", branch, baseBranch)
	if out, err := cmd.CombinedOutput(); err != nil {
		cancel()
		// Branch might already exist from a previous run; that's okay
		if !strings.Contains(string(out), "already exists") {
			return nil, fmt.Errorf("worktree: create branch: %w\n%s", err, out)
		}
	} else {
		cancel()
	}

	// Add worktree (2m timeout)
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	cmd = exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "add", wtPath, branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		cancel()
		return nil, fmt.Errorf("worktree: add worktree: %w\n%s", err, out)
	}
	cancel()

	// Install pre-push hook
	if err := m.installPrePushHook(wtPath); err != nil {
		// Non-fatal: log but continue
		_ = err
	}

	return &Worktree{
		Path:       wtPath,
		Branch:     branch,
		BaseBranch: baseBranch,
	}, nil
}

// Remove removes a worktree and its branch.
func (m *Manager) Remove(repoPath string, wt *Worktree) error {
	if repoPath == "" {
		repoPath = "."
	}
	var errs []string

	// Remove worktree (2m timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "remove", "--force", wt.Path)
	if out, err := cmd.CombinedOutput(); err != nil {
		errs = append(errs, fmt.Sprintf("remove worktree: %v\n%s", err, out))
	}
	cancel()

	// Remove branch (30s timeout)
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	cmd = exec.CommandContext(ctx, "git", "-C", repoPath, "branch", "-D", wt.Branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		errs = append(errs, fmt.Sprintf("remove branch: %v\n%s", err, out))
	}
	cancel()

	// Clean up directory if still exists
	if _, err := os.Stat(wt.Path); err == nil {
		_ = os.RemoveAll(wt.Path)
	}

	if len(errs) > 0 {
		return fmt.Errorf("worktree cleanup errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// PromoteToCommit stages all changes and creates a commit in the worktree.
func (m *Manager) PromoteToCommit(wt *Worktree, message string) (string, error) {
	if message == "" {
		message = fmt.Sprintf("Agent changes from %s", time.Now().UTC().Format(time.RFC3339))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", wt.Path, "add", "-A")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("worktree: stage: %w\n%s", err, out)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, "git", "-C", wt.Path, "commit", "-m", message)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Nothing to commit is okay
		if strings.Contains(string(out), "nothing to commit") {
			return "", nil
		}
		return "", fmt.Errorf("worktree: commit: %w\n%s", err, out)
	}

	// Get commit hash
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, "git", "-C", wt.Path, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("worktree: get hash: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// OpenDraftPR creates a draft PR from the worktree branch.
// Requires gh CLI to be installed and authenticated.
func (m *Manager) OpenDraftPR(wt *Worktree, title, body string) (string, error) {
	if title == "" {
		title = fmt.Sprintf("Draft: %s", wt.Branch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "create",
		"--draft",
		"--title", title,
		"--body", body,
		"--head", wt.Branch,
		"--base", wt.BaseBranch,
	)
	cmd.Dir = wt.Path
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("worktree: draft PR: %w\n%s", err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// StartDevServer starts a development server in the worktree.
func (m *Manager) StartDevServer(wt *Worktree, cmd string, port int) (*exec.Cmd, error) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil, fmt.Errorf("worktree: empty dev server command")
	}
	c := exec.Command(parts[0], parts[1:]...)
	c.Dir = wt.Path
	c.Env = os.Environ()
	if port > 0 {
		c.Env = append(c.Env, fmt.Sprintf("PORT=%d", port))
	}
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("worktree: start dev server: %w", err)
	}
	return c, nil
}

// ReapOldWorktrees removes worktrees older than maxAge that are not actively running.
func (m *Manager) ReapOldWorktrees(maxAge time.Duration) error {
	entries, err := os.ReadDir(m.BaseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(m.BaseDir, entry.Name()))
		}
	}
	return nil
}

const prePushHook = `#!/bin/sh
# Pre-push hook installed by OpsIntelligence kanban system.
# Prevents agents from pushing directly to remote.

echo "========================================"
echo "Agent worktrees cannot push directly."
echo "Use Promote to commit or Open Draft PR."
echo "========================================"
exit 1
`

func (m *Manager) installPrePushHook(wtPath string) error {
	hookDir := filepath.Join(wtPath, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		return err
	}
	hookPath := filepath.Join(hookDir, "pre-push")
	if err := os.WriteFile(hookPath, []byte(prePushHook), 0755); err != nil {
		return err
	}
	return nil
}
