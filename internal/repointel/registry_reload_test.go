package repointel

import (
	"path/filepath"
	"testing"
)

func TestRegistryReload_SeesExternalUpdates(t *testing.T) {
	t.Parallel()

	regPath := filepath.Join(t.TempDir(), "repos.yaml")
	r1, err := NewRegistry(regPath)
	if err != nil {
		t.Fatalf("new registry 1: %v", err)
	}
	entry := RepoEntry{
		ID:       RepoID("github", "acme", "service"),
		Platform: "github",
		Owner:    "acme",
		Name:     "service",
		FullName: "acme/service",
	}
	if err := r1.Add(entry); err != nil {
		t.Fatalf("add: %v", err)
	}

	r2, err := NewRegistry(regPath)
	if err != nil {
		t.Fatalf("new registry 2: %v", err)
	}
	if err := r2.UpdateIndexStatus(entry.ID, IndexReady, "abc123", ""); err != nil {
		t.Fatalf("external update index status: %v", err)
	}

	before, err := r1.Get(entry.ID)
	if err != nil {
		t.Fatalf("get before reload: %v", err)
	}
	if before.IndexStatus == IndexReady {
		t.Fatalf("expected stale in-memory value before reload")
	}

	if err := r1.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	after, err := r1.Get(entry.ID)
	if err != nil {
		t.Fatalf("get after reload: %v", err)
	}
	if after.IndexStatus != IndexReady {
		t.Fatalf("index status after reload: got %s want %s", after.IndexStatus, IndexReady)
	}
	if after.HeadSHA != "abc123" {
		t.Fatalf("head sha after reload: got %q", after.HeadSHA)
	}
}
