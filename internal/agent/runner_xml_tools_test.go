package agent

import "testing"

func TestExtractXMLFunctionCalls_basic(t *testing.T) {
	raw := `I'll fetch PRs.
<function=devops.github.list_prs>
<parameter=owner>
EvoMap
</parameter>
<parameter=repo>evolver</parameter>
<parameter=state>open</parameter>
</function>
</tool_call>`
	got := extractXMLFunctionCalls(raw)
	if len(got) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(got))
	}
	if got[0].ToolName != "devops.github.list_prs" {
		t.Fatalf("tool name: %q", got[0].ToolName)
	}
	m, ok := got[0].ToolInput.(map[string]any)
	if !ok {
		t.Fatalf("input type %T", got[0].ToolInput)
	}
	if m["owner"] != "EvoMap" || m["repo"] != "evolver" || m["state"] != "open" {
		t.Fatalf("args: %#v", m)
	}
	stripped := stripXMLFunctionBlocks(raw)
	if stripped != "I'll fetch PRs." {
		t.Fatalf("stripped: %q", stripped)
	}
}

func TestExtractXMLFunctionCalls_multiple(t *testing.T) {
	raw := `<function=a.b><parameter=x>1</parameter></function> and <function=a.b><parameter=x>2</parameter></function>`
	got := extractXMLFunctionCalls(raw)
	if len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
}
