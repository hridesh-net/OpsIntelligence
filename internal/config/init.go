package config

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed templates/*.md
var embeddedTemplates embed.FS

//go:embed seed/teams/example-team/*.md
var embeddedTeamSeed embed.FS

//go:embed all:seed/prompts
var embeddedPromptsFS embed.FS

// EmbeddedPromptsFS returns the embedded smart-prompt library rooted at
// the repo's `prompts/` tree. Callers should pass the result to
// prompts.Loader{Embedded: ..., EmbeddedRoot: "."} to hydrate the library
// even when the operator has not run `init` yet.
func EmbeddedPromptsFS() (fs.FS, error) {
	return fs.Sub(embeddedPromptsFS, "seed/prompts")
}

// InitializeWorkspace creates the base directories (~/.opsintelligence, tools, skills)
// and drops default opsintelligence.yaml and workspace markdown templates if they don't already exist.
func InitializeWorkspace(configPath string) error {
	dir := filepath.Dir(configPath)

	// Create core directories
	dirs := []string{
		dir,
		filepath.Join(dir, "memory"),
		filepath.Join(dir, "skills"),
		filepath.Join(dir, "tools"),
		filepath.Join(dir, "policies"),
		filepath.Join(dir, "teams", "example-team"),
		filepath.Join(dir, "prompts"),
		filepath.Join(dir, "prompts", "chains"),
		filepath.Join(dir, "workspace", "public"),
	}

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", d, err)
		}
	}

	// Dump embedded markdown templates into the workspace (SOUL.md, IDENTITY.md, etc.)
	entries, err := embeddedTemplates.ReadDir("templates")
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			destPath := filepath.Join(dir, entry.Name())
			if _, statErr := os.Stat(destPath); os.IsNotExist(statErr) {
				if data, readErr := embeddedTemplates.ReadFile(filepath.Join("templates", entry.Name())); readErr == nil {
					_ = os.WriteFile(destPath, data, 0o644)
				}
			}
		}
	}

	// Seed the example team directory with the shipped DevOps policy templates
	// (pr-review, sonar, cicd, secrets-and-safety, README). Existing files are
	// never overwritten — once a team has edited them, they own the content.
	teamDst := filepath.Join(dir, "teams", "example-team")
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

	// Seed the smart-prompt library (pr-review, sonar-triage, cicd-regression,
	// incident-scribe, and the meta/* helpers). Operators can edit files under
	// <state_dir>/prompts/ to override any shipped prompt; existing files are
	// never overwritten.
	promptsRoot := filepath.Join(dir, "prompts")
	if err := seedFromEmbeddedFS(embeddedPromptsFS, "seed/prompts", promptsRoot); err != nil {
		return fmt.Errorf("seed prompts: %w", err)
	}

	// We no longer write a default config.yaml here; the new interactive onboard wizard
	// will generate the custom config once the user inputs their preferences.

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
