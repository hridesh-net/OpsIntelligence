package repointel

import (
	"fmt"
	"testing"
)

func TestSelectKeyFiles_prefersSourceOverTinyTier4(t *testing.T) {
	// Many tiny blobs used to fill the cap before any .go file (tier 4, smallest first).
	tree := []ghTreeEntry{
		{Path: "go.mod", Type: "blob", Size: 400},
		{Path: "internal/service/handler.go", Type: "blob", Size: 8000},
	}
	for i := 0; i < 40; i++ {
		tree = append(tree, ghTreeEntry{
			Path: fmt.Sprintf("config/generated/small-%d.json", i),
			Type: "blob",
			Size: 12,
		})
	}
	out := selectKeyFiles(tree, 6)
	if !containsPath(out, "go.mod") {
		t.Fatalf("expected go.mod in selection, got %v", out)
	}
	if !containsPath(out, "internal/service/handler.go") {
		t.Fatalf("expected handler.go in selection, got %v", out)
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}
