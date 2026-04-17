package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedPromptsFS_HasCoreChains confirms the compiled binary ships
// the full smart-prompt library, not just a subset.
func TestEmbeddedPromptsFS_HasCoreChains(t *testing.T) {
	fsys, err := EmbeddedPromptsFS()
	if err != nil {
		t.Fatalf("subfs: %v", err)
	}
	want := []string{
		"chains/pr-review.yaml",
		"chains/sonar-triage.yaml",
		"chains/cicd-regression.yaml",
		"chains/incident-scribe.yaml",
		"pr-review/gather.md",
		"pr-review/render.md",
		"meta/self-critique.md",
	}
	for _, p := range want {
		if _, err := fs.Stat(fsys, p); err != nil {
			t.Errorf("embedded prompt %q missing: %v", p, err)
		}
	}
}

// TestInitializeWorkspace_SeedsPrompts verifies a freshly initialised
// state dir includes the shipped prompts, and re-running init does not
// overwrite operator edits.
func TestInitializeWorkspace_SeedsPrompts(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "opsintelligence.yaml")
	if err := InitializeWorkspace(cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, rel := range []string{
		"prompts/pr-review/gather.md",
		"prompts/chains/pr-review.yaml",
		"prompts/meta/self-critique.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("seeded %s missing: %v", rel, err)
		}
	}

	// Operator edit must survive a re-init.
	edited := filepath.Join(dir, "prompts/meta/self-critique.md")
	if err := os.WriteFile(edited, []byte("my custom critique"), 0o644); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if err := InitializeWorkspace(cfgPath); err != nil {
		t.Fatalf("reinit: %v", err)
	}
	got, err := os.ReadFile(edited)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "my custom critique" {
		t.Errorf("operator edit was overwritten on reinit: %q", got)
	}
}
