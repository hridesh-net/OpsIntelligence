package openaicompat

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

func collectSSE(t *testing.T, body string) (text string, usage *provider.TokenUsage, toolCalls int) {
	t.Helper()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	ch := make(chan provider.StreamEvent, 64)
	go func() {
		defer close(ch)
		readSSE(context.Background(), resp, ch)
	}()
	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case provider.StreamEventText:
			sb.WriteString(ev.Text)
		case provider.StreamEventToolUse:
			toolCalls++
		case provider.StreamEventDone:
			if ev.Usage != nil {
				if usage == nil {
					usage = &provider.TokenUsage{}
				}
				usage.PromptTokens += ev.Usage.PromptTokens
				usage.CompletionTokens += ev.Usage.CompletionTokens
				usage.TotalTokens += ev.Usage.TotalTokens
			}
		case provider.StreamEventError:
			t.Fatalf("unexpected stream error: %v", ev.Err)
		}
	}
	return sb.String(), usage, toolCalls
}

// Gemini's OpenAI-compat endpoint attaches cumulative usage to EVERY chunk.
// The pre-v1.0.90 parser treated the first usage-bearing chunk as the end of
// the stream and dropped all content — every Gemini reply rendered empty.
func TestReadSSE_GeminiUsageOnEveryChunk(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"Hello","role":"assistant"},"index":0}],"usage":{"completion_tokens":1,"prompt_tokens":10,"total_tokens":11}}

data: {"choices":[{"delta":{"content":" world","role":"assistant"},"finish_reason":"stop","index":0}],"usage":{"completion_tokens":2,"prompt_tokens":10,"total_tokens":12}}

data: [DONE]
`
	text, usage, _ := collectSSE(t, body)
	if text != "Hello world" {
		t.Fatalf("text = %q, want %q", text, "Hello world")
	}
	if usage == nil || usage.TotalTokens != 12 || usage.CompletionTokens != 2 {
		t.Fatalf("usage = %+v, want final cumulative (total 12, completion 2)", usage)
	}
}

// OpenAI convention: usage arrives once, in a dedicated final chunk with
// empty choices (stream_options.include_usage), then [DONE].
func TestReadSSE_OpenAIDedicatedUsageChunk(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"Hi"},"index":0}]}

data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}]}

data: {"choices":[],"usage":{"completion_tokens":5,"prompt_tokens":20,"total_tokens":25}}

data: [DONE]
`
	text, usage, _ := collectSSE(t, body)
	if text != "Hi" {
		t.Fatalf("text = %q, want %q", text, "Hi")
	}
	if usage == nil || usage.TotalTokens != 25 {
		t.Fatalf("usage = %+v, want total 25 counted exactly once", usage)
	}
}

// Tool-call deltas plus per-chunk usage (Gemini shape) must still assemble.
func TestReadSSE_ToolCallsWithPerChunkUsage(t *testing.T) {
	body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"bash","arguments":"{\"comm"}}]},"index":0}],"usage":{"completion_tokens":3,"prompt_tokens":10,"total_tokens":13}}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls\"}"}}]},"finish_reason":"tool_calls","index":0}],"usage":{"completion_tokens":6,"prompt_tokens":10,"total_tokens":16}}

data: [DONE]
`
	_, usage, toolCalls := collectSSE(t, body)
	if toolCalls != 1 {
		t.Fatalf("toolCalls = %d, want 1", toolCalls)
	}
	if usage == nil || usage.TotalTokens != 16 {
		t.Fatalf("usage = %+v, want total 16", usage)
	}
}

// A single SSE line larger than bufio.Scanner's 64KB default must not
// silently truncate the stream.
func TestReadSSE_LargeChunkLine(t *testing.T) {
	big := strings.Repeat("x", 100*1024)
	body := `data: {"choices":[{"delta":{"content":"` + big + `"},"index":0}]}` + "\n\ndata: [DONE]\n"
	text, _, _ := collectSSE(t, body)
	if len(text) != len(big) {
		t.Fatalf("text length = %d, want %d (scanner buffer too small?)", len(text), len(big))
	}
}

// Stream ending without [DONE] (connection close) still surfaces usage.
func TestReadSSE_EOFWithoutDone(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"partial"},"index":0}],"usage":{"completion_tokens":1,"prompt_tokens":5,"total_tokens":6}}
`
	text, usage, _ := collectSSE(t, body)
	if text != "partial" {
		t.Fatalf("text = %q, want %q", text, "partial")
	}
	if usage == nil || usage.TotalTokens != 6 {
		t.Fatalf("usage = %+v, want total 6 from EOF fallback", usage)
	}
}
