package config

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/opsintelligence/opsintelligence/internal/dirs"
)

//go:embed templates/*.md
var embeddedTemplates embed.FS

//go:embed seed/teams/example-team/*.md
var embeddedTeamSeed embed.FS

//go:embed all:seed/prompts
var embeddedPromptsFS embed.FS

//go:embed all:seed/agents
var embeddedAgentsFS embed.FS

// EmbeddedPromptsFS returns the embedded smart-prompt library rooted at
// the repo's `prompts/` tree. Callers should pass the result to
// prompts.Loader{Embedded: ..., EmbeddedRoot: "."} to hydrate the library
// even when the operator has not run `init` yet.
func EmbeddedPromptsFS() (fs.FS, error) {
	return fs.Sub(embeddedPromptsFS, "seed/prompts")
}

// InitializeWorkspace creates canonical state directories (via dirs.Layout),
// seeds prompts/agents/teams under config/, skills, workspace templates, etc.
// It does not recreate legacy flat paths (memory/, tools/ at root); those are
// migrated by dirs.Migrate + dirs.Layout.EnsureAll in the agent entrypoint.
func InitializeWorkspace(configPath string) error {
	root := filepath.Dir(configPath)
	layout := dirs.New(root)
	if err := layout.EnsureAll(); err != nil {
		return fmt.Errorf("ensure state dirs: %w", err)
	}

	// Dump embedded markdown templates into the workspace root (SOUL.md, IDENTITY.md, etc.)
	entries, err := embeddedTemplates.ReadDir("templates")
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			destPath := filepath.Join(layout.Root, entry.Name())
			if _, statErr := os.Stat(destPath); os.IsNotExist(statErr) {
				if data, readErr := embeddedTemplates.ReadFile(filepath.Join("templates", entry.Name())); readErr == nil {
					_ = os.WriteFile(destPath, data, 0o644)
				}
			}
		}
	}

	// Seed the example team directory (config/teams/example-team/)
	teamDst := filepath.Join(layout.Teams, "example-team")
	if err := os.MkdirAll(teamDst, 0o755); err != nil {
		return fmt.Errorf("teams dir: %w", err)
	}
	teamEntries, err := embeddedTeamSeed.ReadDir("seed/teams/example-team")
	if err == nil {
		for _, entry := range teamEntries {
			if entry.IsDir() {
				continue
			}
			destPath := filepath.Join(teamDst, entry.Name())
			if _, statErr := os.Stat(destPath); os.IsNotExist(statErr) {
				if data, readErr := embeddedTeamSeed.ReadFile(filepath.Join("seed/teams/example-team", entry.Name())); readErr == nil {
					_ = os.WriteFile(destPath, data, 0o644)
				}
			}
		}
	}

	// Smart-prompt library under config/prompts/
	if err := seedFromEmbeddedFS(embeddedPromptsFS, "seed/prompts", layout.Prompts); err != nil {
		return fmt.Errorf("seed prompts: %w", err)
	}

	// Agent execution pipelines under config/agents/
	if err := seedFromEmbeddedFS(embeddedAgentsFS, "seed/agents", layout.Agents); err != nil {
		return fmt.Errorf("seed agent flows: %w", err)
	}

	return nil
}

// seedFromEmbeddedFS walks an embedded sub-tree rooted at `root` and copies
// every file to `dstBase`, preserving sub-directories and skipping files
// that already exist on disk.
func seedFromEmbeddedFS(src embed.FS, root, dstBase string) error {
	return fs.WalkDir(src, root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := path[len(root):]
		rel = filepath.FromSlash(rel)
		dst := filepath.Join(dstBase, rel)
		if d.IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			return nil
		}
		if _, err := os.Stat(dst); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		data, err := src.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}
