package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/localintel"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

const (
	retryTrimToolResultBytes = 6000
	retryTrimTextBytes       = 12000
)

// errRecoverablePrimaryStream is true when a retry or LocalIntel fallback might help.
func errRecoverablePrimaryStream(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "length limit exceeded") {
		return true
	}
	if strings.Contains(s, "validationexception") && (strings.Contains(s, "too large") || strings.Contains(s, "limit")) {
		return true
	}
	if strings.Contains(s, "throttling") || strings.Contains(s, "timeout") || strings.Contains(s, "timed out") {
		return true
	}
	if strings.Contains(s, "429") || strings.Contains(s, "503") || strings.Contains(s, "502") || strings.Contains(s, "500") {
		return true
	}
	var pe *provider.ProviderError
	if errors.As(err, &pe) && pe != nil && pe.Retryable {
		return true
	}
	return false
}

// deepTruncateMessagesForRetry returns a defensive copy of msgs with smaller text / tool payloads.
func deepTruncateMessagesForRetry(msgs []provider.Message, toolMaxBytes, textMaxBytes int) []provider.Message {
	out := make([]provider.Message, len(msgs))
	for i := range msgs {
		out[i].Role = msgs[i].Role
		out[i].Name = msgs[i].Name
		out[i].Content = make([]provider.ContentPart, len(msgs[i].Content))
		for j, cp := range msgs[i].Content {
			out[i].Content[j] = cp
			switch cp.Type {
			case provider.ContentTypeToolResult:
				orig := cp.ToolResultContent
				t := truncateUTF8(orig, toolMaxBytes)
				if len(orig) > len(t) {
					t += "\n\n[_truncated before retry for model request size_]"
				}
				out[i].Content[j].ToolResultContent = t
			case provider.ContentTypeText:
				orig := cp.Text
				t := truncateUTF8(orig, textMaxBytes)
				if len(orig) > len(t) {
					t += "\n\n[_truncated before retry for model request size_]"
				}
				out[i].Content[j].Text = t
			}
		}
	}
	return out
}

func (r *Runner) workingMemoryDigest(maxBytes int) string {
	if r.working == nil || maxBytes <= 0 {
		return ""
	}
	msgs := r.working.Messages()
	var b strings.Builder
	for i := len(msgs) - 1; i >= 0 && b.Len() < maxBytes; i-- {
		m := msgs[i]
		frag := string(m.Role) + ": " + m.Content
		if len(m.Parts) > 0 {
			var ps strings.Builder
			for _, p := range m.Parts {
				switch p.Type {
				case provider.ContentTypeToolResult:
					ps.WriteString("[tool result] ")
					ps.WriteString(truncateUTF8(p.ToolResultContent, 800))
					ps.WriteString("\n")
				case provider.ContentTypeToolUse:
					ps.WriteString("[tool] ")
					ps.WriteString(p.ToolName)
					ps.WriteString("\n")
				}
			}
			if ps.Len() > 0 {
				frag += "\n" + ps.String()
			}
		}
		frag = truncateUTF8(frag, 4000)
		if b.Len()+len(frag)+1 > maxBytes {
			frag = truncateUTF8(frag, intMax(0, maxBytes-b.Len()-16))
		}
		b.WriteString(frag)
		b.WriteByte('\n')
	}
	return b.String()
}

func intMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// localIntelFallbackStream runs a single local completion and replays it as a provider stream.
// Returns nil when LocalIntel is not configured, or the engine did not load (no GGUF / wrong build).
func (r *Runner) localIntelFallbackStream(ctx context.Context, userMessage string, lastCloudErr error) <-chan provider.StreamEvent {
	if !r.localIntelPresent() {
		return nil
	}
	eng := r.ensureLocalIntelEngine()
	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		sys := `You are the on-device assistant (Gemma). The primary cloud model failed for this turn (size limit, provider error, or network).
Answer the user's request as well as you can from the message and session digest only. Be concise and honest about limits (no live tools).
If they asked for a PR review, summarize risks from any digest text you see; do not invent file contents.`

		var ub strings.Builder
		if lastCloudErr != nil {
			ub.WriteString("Primary model error (for context): ")
			ub.WriteString(lastCloudErr.Error())
			ub.WriteString("\n\n")
		}
		ub.WriteString("User message:\n")
		ub.WriteString(userMessage)
		if d := strings.TrimSpace(r.workingMemoryDigest(14 * 1024)); d != "" {
			ub.WriteString("\n\n## Session digest (truncated)\n")
			ub.WriteString(d)
		}
		if adv := strings.TrimSpace(r.localIntelScratch); adv != "" {
			ub.WriteString("\n\n## Earlier on-device advisory\n")
			ub.WriteString(truncateUTF8(adv, 4000))
		}

		maxTok := r.cfg.LocalIntel.MaxTokens
		if maxTok <= 0 {
			maxTok = 1024
		}
		out, err := eng.Complete(ctx, localintel.Request{
			System:    sys,
			User:      ub.String(),
			MaxTokens: maxTok,
		})
		if err != nil {
			ch <- provider.StreamEvent{Type: provider.StreamEventError, Err: fmt.Errorf("local_intel fallback: %w", err)}
			return
		}
		prefix := "(Local model fallback — cloud model unavailable for this turn.)\n\n"
		combined := prefix + strings.TrimSpace(out)
		if combined == prefix {
			ch <- provider.StreamEvent{Type: provider.StreamEventError, Err: fmt.Errorf("local_intel fallback: empty completion")}
			return
		}
		ch <- provider.StreamEvent{Type: provider.StreamEventText, Text: combined}
		ch <- provider.StreamEvent{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{}}
	}()
	return ch
}

// openPrimaryModelStream opens the cloud provider stream with at most one trimmed retry,
// then falls back to LocalIntel on the third attempt when enabled.
func (r *Runner) openPrimaryModelStream(ctx context.Context, userMessage string, req *provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		useReq := req
		var cloned provider.CompletionRequest
		if attempt == 1 {
			cloned = *req
			cloned.Messages = deepTruncateMessagesForRetry(req.Messages, retryTrimToolResultBytes, retryTrimTextBytes)
			useReq = &cloned
		}
		stream, err := r.provider.Stream(ctx, useReq)
		if err == nil {
			if attempt > 0 {
				r.log.Info("agent: primary model stream opened after retry", zap.Int("attempt", attempt+1))
			}
			return stream, nil
		}
		lastErr = err
		if attempt == 0 {
			if !errRecoverablePrimaryStream(err) {
				return nil, err
			}
			r.log.Warn("agent: primary model stream failed; retrying with trimmed context", zap.Error(err))
			time.Sleep(150 * time.Millisecond)
			continue
		}
		// Second primary attempt failed.
		if !errRecoverablePrimaryStream(err) {
			return nil, err
		}
		break
	}
	if errRecoverablePrimaryStream(lastErr) {
		if fs := r.localIntelFallbackStream(ctx, userMessage, lastErr); fs != nil {
			r.log.Warn("agent: using LocalIntel stream fallback after primary failures", zap.Error(lastErr))
			return fs, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("agent: model stream: unknown error")
}
