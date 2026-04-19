package bedrock

import (
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

func TestBedrockNormalizeToolName(t *testing.T) {
	if got := bedrockNormalizeToolName("devops.github.pull_request"); got != "devops_github_pull_request" {
		t.Fatalf("normalize dotted: got %q", got)
	}
	if got := bedrockNormalizeToolName("read_file"); got != "read_file" {
		t.Fatalf("unchanged: got %q", got)
	}
}

func TestNewBedrockToolAliases_collision(t *testing.T) {
	a := newBedrockToolAliases([]provider.ToolDef{
		{Name: "a.b"},
		{Name: "a_b"},
	})
	if a.toAWSName("a.b") != "a_b" {
		t.Fatalf("first tool: got %q", a.toAWSName("a.b"))
	}
	if want := "a_b_2"; a.toAWSName("a_b") != want {
		t.Fatalf("second tool: got %q want %q", a.toAWSName("a_b"), want)
	}
	if a.fromAWSName("a_b") != "a.b" {
		t.Fatalf("reverse first: got %q", a.fromAWSName("a_b"))
	}
	if a.fromAWSName("a_b_2") != "a_b" {
		t.Fatalf("reverse second: got %q", a.fromAWSName("a_b_2"))
	}
}

func TestBedrockToolAliases_nilReceiver(t *testing.T) {
	var a *bedrockToolAliases
	if got := a.toAWSName("x.y"); got != "x_y" {
		t.Fatalf("nil aliases toAWSName: got %q", got)
	}
	if got := a.fromAWSName("anything"); got != "anything" {
		t.Fatalf("nil aliases fromAWSName: got %q", got)
	}
}
