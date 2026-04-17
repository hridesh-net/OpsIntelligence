package configsvc_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/configsvc"
)

const minimalConfig = `
version: 1
state_dir: /tmp/opsi-test
gateway:
  host: 127.0.0.1
  port: 18790
providers:
  ollama:
    base_url: http://127.0.0.1:11434
`

func writeCfg(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(minimalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSetSkillEnabledRoundTrip(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "opsintelligence.yaml")
	writeCfg(t, cfgPath)
	svc := configsvc.New(cfgPath)
	if _, err := svc.SetSkillEnabled(context.Background(), "devops", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := svc.SetSkillEnabled(context.Background(), "devops", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	snap, err := svc.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(snap.Config.Agent.EnabledSkills) != 0 {
		t.Fatalf("expected empty enabled_skills, got %#v", snap.Config.Agent.EnabledSkills)
	}
}

func TestMCPAddRemove(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "opsintelligence.yaml")
	writeCfg(t, cfgPath)
	svc := configsvc.New(cfgPath)

	if _, err := svc.AddMCPClient(context.Background(), config.MCPClientConfig{
		Name:      "filesystem",
		Transport: "stdio",
		Command:   "npx @modelcontextprotocol/server-filesystem /tmp",
	}); err != nil {
		t.Fatalf("add client: %v", err)
	}

	if _, err := svc.RemoveMCPClient(context.Background(), "filesystem"); err != nil {
		t.Fatalf("remove client: %v", err)
	}
	snap, err := svc.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(snap.Config.MCP.Clients) != 0 {
		t.Fatalf("expected no clients after removal, got %d", len(snap.Config.MCP.Clients))
	}
}

func TestUpdateWithRevisionConflict(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "opsintelligence.yaml")
	writeCfg(t, cfgPath)
	svc := configsvc.New(cfgPath)
	snap, err := svc.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateWithRevision(context.Background(), snap.Revision, func(cfg *config.Config) error {
		cfg.Gateway.Port = 19999
		return nil
	}); err != nil {
		t.Fatalf("update with matching revision should succeed: %v", err)
	}
}

func TestRevisionConflictSentinel(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "opsintelligence.yaml")
	writeCfg(t, cfgPath)
	svc := configsvc.New(cfgPath)
	snap, err := svc.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSkillEnabled(context.Background(), "a", true); err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateWithRevision(context.Background(), snap.Revision, func(cfg *config.Config) error {
		cfg.Gateway.Port = 18888
		return nil
	})
	if !errors.Is(err, configsvc.ErrRevisionConflict) {
		t.Fatalf("expected revision conflict, got: %v", err)
	}
}
