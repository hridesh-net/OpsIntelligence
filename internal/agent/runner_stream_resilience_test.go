package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

func TestErrRecoverablePrimaryStream(t *testing.T) {
	t.Parallel()
	if !errRecoverablePrimaryStream(fmt.Errorf(`ValidationException: length limit exceeded`)) {
		t.Fatal("expected length limit to be recoverable")
	}
	if !errRecoverablePrimaryStream(fmt.Errorf("ThrottlingException: slow down")) {
		t.Fatal("expected throttling to be recoverable")
	}
	if errRecoverablePrimaryStream(fmt.Errorf("InvalidSignatureException")) {
		t.Fatal("did not expect signature errors to be recoverable")
	}
	pe := &provider.ProviderError{Provider: "bedrock", Message: "x", Retryable: true, Err: fmt.Errorf("inner")}
	if !errRecoverablePrimaryStream(pe) {
		t.Fatal("expected ProviderError Retryable to be recoverable")
	}
}

func TestDeepTruncateMessagesForRetry(t *testing.T) {
	t.Parallel()
	long := string(make([]byte, 20000))
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.ContentPart{
			{Type: provider.ContentTypeText, Text: long},
			{Type: provider.ContentTypeToolResult, ToolResultContent: long, ToolResultID: "t1"},
		}},
	}
	out := deepTruncateMessagesForRetry(msgs, 100, 200)
	if len(out) != 1 || len(out[0].Content) != 2 {
		t.Fatalf("unexpected shape: %#v", out)
	}
	if len(out[0].Content[0].Text) >= len(long) {
		t.Fatal("expected text truncated")
	}
	if len(out[0].Content[1].ToolResultContent) >= len(long) {
		t.Fatal("expected tool result truncated")
	}
	if !strings.Contains(out[0].Content[0].Text, "_truncated before retry") {
		t.Fatal("expected truncation marker on text")
	}
}
