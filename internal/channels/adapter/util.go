package adapter

import (
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// OutboundBody extracts the plain-text body from an OutboundMessage, preferring
// msg.Text and falling back to concatenated text ContentParts.
func OutboundBody(msg OutboundMessage) string {
	if msg.Text != "" {
		return msg.Text
	}
	var b strings.Builder
	for _, p := range msg.Parts {
		if p.Type == provider.ContentTypeText && p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// SplitMessage splits s into chunks no longer than maxLen, breaking on newlines
// then spaces before falling back to a hard cut at maxLen.
func SplitMessage(s string, maxLen int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for len(s) > maxLen {
		cut := strings.LastIndex(s[:maxLen], "\n")
		if cut < 120 {
			cut = strings.LastIndex(s[:maxLen], " ")
		}
		if cut < 120 {
			cut = maxLen
		}
		out = append(out, strings.TrimSpace(s[:cut]))
		s = strings.TrimSpace(s[cut:])
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}
