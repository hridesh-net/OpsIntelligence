// Package runtrace provides append-only NDJSON execution tracing for
// operator visibility (tools, chains, model routing) without affecting
// agent control flow.
package runtrace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type outputPathKey struct{}

// WithOutputPath attaches the NDJSON trace file path to ctx so shared tools
// (e.g. chain_run) log to the same file as the active agent invocation.
func WithOutputPath(ctx context.Context, path string) context.Context {
	path = strings.TrimSpace(path)
	if path == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, outputPathKey{}, path)
}

// OutputPathFrom returns the trace path set via WithOutputPath, or "".
func OutputPathFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(outputPathKey{}).(string)
	return strings.TrimSpace(v)
}

var mu sync.Mutex

// Append writes one JSON object as a single line to path. path must be
// absolute after config resolution. Failures are silent so tracing never
// breaks the agent.
func Append(path string, event map[string]any) {
	if path == "" {
		return
	}
	if event == nil {
		event = map[string]any{}
	}
	event["t"] = time.Now().UTC().Format(time.RFC3339Nano)

	line, err := json.Marshal(event)
	if err != nil {
		return
	}

	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

// InferBackend labels the primary completion backend for monitoring.
func InferBackend(providerName, model string, localIntelEnabled, localAdvisoryNonEmpty bool) string {
	p := strings.ToLower(strings.TrimSpace(providerName))
	m := strings.ToLower(model)
	switch p {
	case "ollama", "lmstudio", "vllm":
		return "local_primary"
	case "":
		if strings.Contains(m, "ollama/") || strings.Contains(m, "lmstudio/") {
			return "local_primary"
		}
	}
	if localIntelEnabled && localAdvisoryNonEmpty {
		return "remote_primary_with_local_advisory"
	}
	if localIntelEnabled {
		return "remote_primary_local_intel_enabled"
	}
	return "remote_primary"
}
