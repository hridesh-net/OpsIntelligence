package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

type fakeTool struct{ ran bool }

func (f *fakeTool) Definition() provider.ToolDef {
	return provider.ToolDef{Name: "test.echo", Description: "echo"}
}

func (f *fakeTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	f.ran = true
	return "ran", nil
}

func newRBACTestRunner(ft *fakeTool) *Runner {
	reg := NewToolRegistry()
	reg.Register(ft)
	return &Runner{
		tools: reg,
		log:   zap.NewNop(),
	}
}

func echoCall() provider.ContentPart {
	return provider.ContentPart{ToolName: "test.echo", ToolInput: map[string]any{}}
}

// No principal attached (CLI REPL / channel adapters) — trusted, tool runs.
func TestExecuteTool_NoPrincipal_Allowed(t *testing.T) {
	ft := &fakeTool{}
	r := newRBACTestRunner(ft)
	out := r.executeTool(context.Background(), echoCall())
	if !ft.ran || out != "ran" {
		t.Fatalf("expected tool to run, got %q (ran=%v)", out, ft.ran)
	}
}

// User principal without agent.invoke — denied before execution.
func TestExecuteTool_UserWithoutPermission_Denied(t *testing.T) {
	ft := &fakeTool{}
	r := newRBACTestRunner(ft)
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
		Type: auth.PrincipalUser, UserID: "u1", Username: "viewer",
		Permissions: []string{"dashboard.view", "chat.use"},
	})
	out := r.executeTool(ctx, echoCall())
	if ft.ran {
		t.Fatal("tool executed despite missing agent.invoke")
	}
	if !strings.HasPrefix(out, "[RBAC]") {
		t.Fatalf("expected RBAC denial message, got %q", out)
	}
}

// User principal WITH agent.invoke — runs.
func TestExecuteTool_UserWithPermission_Allowed(t *testing.T) {
	ft := &fakeTool{}
	r := newRBACTestRunner(ft)
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
		Type: auth.PrincipalUser, UserID: "u2", Username: "dev",
		Permissions: []string{"agent.invoke"},
	})
	if out := r.executeTool(ctx, echoCall()); !ft.ran || out != "ran" {
		t.Fatalf("expected tool to run, got %q", out)
	}
}

// System principal bypasses RBAC.
func TestExecuteTool_SystemPrincipal_Allowed(t *testing.T) {
	ft := &fakeTool{}
	r := newRBACTestRunner(ft)
	ctx := auth.WithPrincipal(context.Background(), auth.SystemPrincipal("gateway-cli"))
	if out := r.executeTool(ctx, echoCall()); !ft.ran || out != "ran" {
		t.Fatalf("expected tool to run, got %q", out)
	}
}

func TestAuditActor(t *testing.T) {
	if got := auditActor(context.Background()); got != "" {
		t.Fatalf("no principal: want empty actor, got %q", got)
	}
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
		Type: auth.PrincipalUser, UserID: "u1", Username: "alice",
	})
	if got := auditActor(ctx); got != "user:alice" {
		t.Fatalf("want user:alice, got %q", got)
	}
	ctx = auth.WithPrincipal(context.Background(), auth.SystemPrincipal("cron"))
	if got := auditActor(ctx); got != "system:cron" {
		t.Fatalf("want system:cron, got %q", got)
	}
}
