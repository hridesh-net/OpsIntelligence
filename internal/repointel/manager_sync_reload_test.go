package repointel

import (
	"path/filepath"
	"testing"
)

func TestManagerSyncRepo_ReloadsExternalRegistryBeforeEnqueue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	regPath := filepath.Join(root, "repos.yaml")
	memDir := filepath.Join(root, "memory")

	mgr, err := NewManager(ManagerConfig{
		RegistryPath: regPath,
		MemoryDir:    memDir,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	// Simulate another process (CLI) adding a repo entry after manager startup.
	extReg, err := NewRegistry(regPath)
	if err != nil {
		t.Fatalf("new external registry: %v", err)
	}
	id := RepoID("github", "acme", "service")
	if err := extReg.Add(RepoEntry{
		ID:       id,
		Platform: "github",
		Owner:    "acme",
		Name:     "service",
		FullName: "acme/service",
	}); err != nil {
		t.Fatalf("external add: %v", err)
	}

	if err := mgr.SyncRepo(id); err != nil {
		t.Fatalf("sync repo should succeed after reload, got: %v", err)
	}
}
