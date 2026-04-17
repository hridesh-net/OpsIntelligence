package prompts

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// fakeProvider is a deterministic stub that echoes the user message
// with a per-step marker so we can assert chaining order.
type fakeProvider struct {
	name  string
	calls int
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Complete(_ context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	f.calls++
	var user string
	for _, m := range req.Messages {
		if m.Role == provider.RoleUser {
			for _, c := range m.Content {
				if c.Type == provider.ContentTypeText {
					user += c.Text
				}
			}
		}
	}
	out := "[" + req.Model + "#" + itoa(f.calls) + "] sys=" + req.SystemPrompt + " | user=" + user
	return &provider.CompletionResponse{
		Model:        req.Model,
		Content:      []provider.ContentPart{{Type: provider.ContentTypeText, Text: out}},
		FinishReason: provider.FinishReasonStop,
		Usage:        provider.TokenUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}, nil
}
func (f *fakeProvider) Stream(_ context.Context, _ *provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}
func (f *fakeProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) { return nil, nil }
func (f *fakeProvider) HealthCheck(_ context.Context) error                        { return nil }
func (f *fakeProvider) Embed(_ context.Context, _ string, _ string) ([]float32, error) {
	return nil, nil
}
func (f *fakeProvider) ValidateModel(_ context.Context, _ string) error { return nil }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestLoader_ParsesFrontmatterAndBody(t *testing.T) {
	fsys := fstest.MapFS{
		"greet/hello.md": {Data: []byte(`---
id: greet/hello
name: Say hi
purpose: Tiny greeter
system: "You are a helpful greeter."
---

Say hi to {{.name}}.
`)},
	}
	lib, err := Loader{Embedded: fsys, EmbeddedRoot: "."}.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p, ok := lib.Prompt("greet/hello")
	if !ok {
		t.Fatal("prompt greet/hello not loaded")
	}
	if p.Name != "Say hi" || p.System != "You are a helpful greeter." {
		t.Fatalf("unexpected prompt: %+v", p)
	}
	if !strings.Contains(p.Body, "{{.name}}") {
		t.Fatalf("body missing template: %q", p.Body)
	}
}

func TestRunner_RunChain_PipesPrevBetweenSteps(t *testing.T) {
	fsys := fstest.MapFS{
		"step1.md": {Data: []byte(`---
id: step1
system: "sys1"
---
first {{.topic}}
`)},
		"step2.md": {Data: []byte(`---
id: step2
system: "sys2"
---
second building on: {{.prev}}
`)},
		"chains/demo.yaml": {Data: []byte(`id: demo
name: Demo
purpose: two-step demo
inputs: [topic]
steps:
  - step1
  - step2
`)},
	}
	lib, err := Loader{Embedded: fsys, EmbeddedRoot: "."}.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := &Runner{Provider: &fakeProvider{name: "fake"}, Lib: lib, DefaultModel: "m1"}
	result, err := r.RunChain(context.Background(), "demo", map[string]any{"topic": "widgets"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(result.Steps))
	}
	if !strings.Contains(result.Steps[0].Output, "first widgets") {
		t.Errorf("step 1 missing topic: %q", result.Steps[0].Output)
	}
	if !strings.Contains(result.Steps[1].Output, "second building on:") {
		t.Errorf("step 2 missing prev-chaining: %q", result.Steps[1].Output)
	}
	if result.Final != result.Steps[1].Output {
		t.Errorf("Final should equal last step output")
	}
	if result.Usage.TotalTokens != 60 {
		t.Errorf("want aggregated usage 60, got %d", result.Usage.TotalTokens)
	}
}

func TestRunner_RunChain_RejectsMissingInput(t *testing.T) {
	fsys := fstest.MapFS{
		"step1.md": {Data: []byte(`---
id: step1
---
go
`)},
		"chains/need.yaml": {Data: []byte(`id: need
inputs: [pr_url]
steps: [step1]
`)},
	}
	lib, _ := Loader{Embedded: fsys, EmbeddedRoot: "."}.Load()
	r := &Runner{Provider: &fakeProvider{}, Lib: lib, DefaultModel: "m"}
	if _, err := r.RunChain(context.Background(), "need", nil); err == nil {
		t.Fatal("expected missing-input error")
	}
}

func TestLibrary_Index_ShowsChainsAndMetaPrompts(t *testing.T) {
	lib := NewLibrary()
	_ = lib.AddPrompt(&SmartPrompt{ID: "meta/self-critique", Purpose: "Flag missing evidence", Tags: []string{"meta"}})
	_ = lib.AddPrompt(&SmartPrompt{ID: "pr-review/gather", Purpose: "Collect PR evidence"})
	_ = lib.AddChain(&Chain{ID: "pr-review", Purpose: "End-to-end PR review", Steps: []string{"pr-review/gather"}})
	idx := lib.Index()
	if !strings.Contains(idx, "chain:pr-review") {
		t.Errorf("index missing chain:pr-review: %q", idx)
	}
	if !strings.Contains(idx, "prompt:meta/self-critique") {
		t.Errorf("index missing meta prompt: %q", idx)
	}
	if strings.Contains(idx, "prompt:pr-review/gather") {
		t.Errorf("non-meta prompts should not appear in index: %q", idx)
	}
}

func TestRunner_RunPrompt_SingleShot(t *testing.T) {
	fsys := fstest.MapFS{
		"meta/echo.md": {Data: []byte(`---
id: meta/echo
system: "be terse"
tags: [meta]
---
echo: {{.msg}}
`)},
	}
	lib, _ := Loader{Embedded: fsys, EmbeddedRoot: "."}.Load()
	r := &Runner{Provider: &fakeProvider{}, Lib: lib, DefaultModel: "m"}
	step, err := r.RunPrompt(context.Background(), "meta/echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(step.Output, "echo: hi") {
		t.Errorf("unexpected output: %q", step.Output)
	}
}
