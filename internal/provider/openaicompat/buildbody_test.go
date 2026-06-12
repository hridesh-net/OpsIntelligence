package openaicompat

import (
	"encoding/json"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// decodeBody unmarshals the request body buildBody produced so tests can
// assert on the OpenAI chat schema it should emit.
func decodeBody(t *testing.T, p *Provider, req *provider.CompletionRequest) map[string]any {
	t.Helper()
	raw, err := p.buildBody(req, true)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return out
}

// A multi-turn conversation where the assistant called a tool and a tool
// result came back must serialize as OpenAI's schema: tool_calls on the
// assistant message and a role:"tool" message — NOT {"type":"tool_use"} /
// {"type":"tool_result"} content parts (Gemini's compat endpoint rejects
// those with HTTP 400 "Invalid content part type").
func TestBuildBody_ToolCallRoundTrip(t *testing.T) {
	p := New(Config{Name: "gemini", DefaultModel: "gemini-2.5-flash"})
	req := &provider.CompletionRequest{
		Model:        "gemini-2.5-flash",
		SystemPrompt: "You are helpful.",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentPart{
				{Type: provider.ContentTypeText, Text: "make a file"},
			}},
			{Role: provider.RoleAssistant, Content: []provider.ContentPart{
				{Type: provider.ContentTypeText, Text: "On it."},
				{Type: provider.ContentTypeToolUse, ToolUseID: "call_1", ToolName: "write_file",
					ToolInput: map[string]any{"path": "X.md", "content": "hi"}},
			}},
			{Role: provider.RoleTool, Content: []provider.ContentPart{
				{Type: provider.ContentTypeText, Text: "Written 2 bytes"},
				{Type: provider.ContentTypeToolResult, ToolResultID: "call_1", ToolResultContent: "Written 2 bytes"},
			}},
		},
	}

	body := decodeBody(t, p, req)
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 4 { // system + user + assistant + tool
		t.Fatalf("want 4 messages, got %d: %+v", len(msgs), msgs)
	}

	// Assistant message: tool_calls present, no content-part array.
	asst, _ := msgs[2].(map[string]any)
	if asst["role"] != "assistant" {
		t.Fatalf("msg[2] role = %v, want assistant", asst["role"])
	}
	tcs, ok := asst["tool_calls"].([]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("assistant tool_calls missing/empty: %+v", asst)
	}
	tc, _ := tcs[0].(map[string]any)
	if tc["type"] != "function" || tc["id"] != "call_1" {
		t.Fatalf("tool_call shape wrong: %+v", tc)
	}
	fn, _ := tc["function"].(map[string]any)
	if fn["name"] != "write_file" {
		t.Fatalf("function name = %v", fn["name"])
	}
	// arguments must be a JSON-encoded STRING, not an object.
	args, ok := fn["arguments"].(string)
	if !ok {
		t.Fatalf("function.arguments must be a string, got %T", fn["arguments"])
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil || parsed["path"] != "X.md" {
		t.Fatalf("arguments not valid JSON-string with path: %q", args)
	}
	if _, isStr := asst["content"].(string); !isStr {
		t.Fatalf("assistant content must be a string, got %T", asst["content"])
	}

	// Tool message: role:"tool" + tool_call_id + string content.
	tool, _ := msgs[3].(map[string]any)
	if tool["role"] != "tool" {
		t.Fatalf("msg[3] role = %v, want tool", tool["role"])
	}
	if tool["tool_call_id"] != "call_1" {
		t.Fatalf("tool_call_id = %v, want call_1", tool["tool_call_id"])
	}
	if tool["content"] != "Written 2 bytes" {
		t.Fatalf("tool content = %v", tool["content"])
	}

	// Guard: no message may carry a raw tool_use/tool_result content part.
	if blob := string(mustJSON(t, body)); containsAny(blob, `"type":"tool_use"`, `"type":"tool_result"`) {
		t.Fatalf("body leaked a raw content-part type:\n%s", blob)
	}
}

// A plain text turn still serializes content as a string.
func TestBuildBody_PlainText(t *testing.T) {
	p := New(Config{Name: "gemini", DefaultModel: "m"})
	req := &provider.CompletionRequest{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentPart{
				{Type: provider.ContentTypeText, Text: "hi"},
			}},
		},
	}
	body := decodeBody(t, p, req)
	msgs, _ := body["messages"].([]any)
	m0, _ := msgs[0].(map[string]any)
	if m0["content"] != "hi" {
		t.Fatalf("content = %v, want hi", m0["content"])
	}
	if _, hasTC := m0["tool_calls"]; hasTC {
		t.Fatalf("plain text message should not have tool_calls")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
