package runtrace

import (
	"context"
	"testing"
)

func TestInferBackend(t *testing.T) {
	if g := InferBackend("ollama", "ollama/llama3", false, false); g != "local_primary" {
		t.Fatalf("got %q", g)
	}
	if g := InferBackend("openai", "gpt-4o", true, true); g != "remote_primary_with_local_advisory" {
		t.Fatalf("got %q", g)
	}
	if g := InferBackend("openai", "gpt-4o", true, false); g != "remote_primary_local_intel_enabled" {
		t.Fatalf("got %q", g)
	}
	if g := InferBackend("anthropic", "claude", false, false); g != "remote_primary" {
		t.Fatalf("got %q", g)
	}
}

func TestWithOutputPath(t *testing.T) {
	ctx := context.Background()
	ctx = WithOutputPath(ctx, "/tmp/runtrace-sub.ndjson")
	if got := OutputPathFrom(ctx); got != "/tmp/runtrace-sub.ndjson" {
		t.Fatalf("OutputPathFrom: got %q", got)
	}
	if got := OutputPathFrom(context.Background()); got != "" {
		t.Fatalf("missing ctx: got %q want empty", got)
	}
	if WithOutputPath(nil, "/x") != nil {
		t.Fatal("nil ctx should be unchanged")
	}
}
