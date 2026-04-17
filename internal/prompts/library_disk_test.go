package prompts

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestLoader_LoadsRepoPrompts verifies the shipped library parses cleanly
// and all chains reference known prompts. This guards against typos in
// the YAML frontmatter or chain step lists when new prompts are added.
func TestLoader_LoadsRepoPrompts(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir := filepath.Join(repoRoot, "prompts")

	lib, err := Loader{Dir: dir}.Load()
	if err != nil {
		t.Fatalf("load %s: %v", dir, err)
	}
	wantChains := []string{"pr-review", "sonar-triage", "cicd-regression", "incident-scribe"}
	for _, id := range wantChains {
		c, ok := lib.Chain(id)
		if !ok {
			t.Errorf("chain %q missing", id)
			continue
		}
		for _, step := range c.Steps {
			if _, ok := lib.Prompt(step); !ok {
				t.Errorf("chain %q references unknown prompt %q", id, step)
			}
		}
	}
	wantMeta := []string{"meta/self-critique", "meta/evidence-extractor", "meta/plan-then-act"}
	for _, id := range wantMeta {
		if _, ok := lib.Prompt(id); !ok {
			t.Errorf("meta prompt %q missing", id)
		}
	}
}
