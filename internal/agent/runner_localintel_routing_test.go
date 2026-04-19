package agent

import "testing"

func TestParseLocalIntelRoutingOutput(t *testing.T) {
	t.Parallel()
	tools, focus := parseLocalIntelRoutingOutput(`
Some chatter
TOOLS: read_file, devops.github.pull_request, chain_run
SKILLS_FOCUS: prioritize gh-pr-review for this PR question
`)
	if len(tools) != 3 || tools[0] != "read_file" {
		t.Fatalf("tools: %#v", tools)
	}
	if focus == "" || focus[:4] == "Some" {
		t.Fatalf("focus: %q", focus)
	}
	tools2, _ := parseLocalIntelRoutingOutput("TOOLS: bash\nskills: none\n")
	if len(tools2) != 1 || tools2[0] != "bash" {
		t.Fatalf("tools2: %#v", tools2)
	}
}
