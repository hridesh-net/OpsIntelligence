package agent

import (
	"context"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/localintel"
)

const defaultSmartRoutingMaxTokens = 384

const smartRoutingSystem = `You are a routing layer for a larger agent. Your job is to pick the most relevant tool IDs and a one-line skill focus before the cloud model runs.

Output rules (strict):
1) First line must start with exactly: TOOLS:
2) Second line must start with exactly: SKILLS_FOCUS:

TOOLS line: comma-separated tool ids only (no spaces around commas is ok). Use at most 12 ids. Every id MUST appear verbatim in the TOOL_IDS list below. Order most relevant first.

SKILLS_FOCUS line: one short sentence (max 200 characters) saying which active skills matter for the user message, or the word none if the skills block is irrelevant.

Do not output markdown, code fences, or any other lines.`

// prepareLocalIntelSmartRouting runs Gemma to suggest tools and a skill-focus line for this turn.
func (r *Runner) prepareLocalIntelSmartRouting(ctx context.Context, userMessage string) {
	r.localIntelRoutingTools = nil
	r.localIntelRoutingSkillFocus = ""
	if !r.cfg.LocalIntel.SmartRouting || !r.localIntelPresent() {
		return
	}
	if r.tools == nil {
		return
	}
	eng := r.ensureLocalIntelEngine()

	names := r.toolNamesForRouting()
	if len(names) == 0 {
		return
	}
	var toolList strings.Builder
	for _, n := range names {
		toolList.WriteString(n)
		toolList.WriteByte('\n')
	}
	toolBlock := truncateUTF8(toolList.String(), 12000)

	var ub strings.Builder
	ub.WriteString("USER MESSAGE:\n")
	ub.WriteString(userMessage)
	ub.WriteString("\n\nACTIVE SKILLS CONTEXT (may be short):\n")
	if sk := strings.TrimSpace(r.cfg.ActiveSkillsContext); sk != "" {
		ub.WriteString(truncateUTF8(sk, 6000))
	} else {
		ub.WriteString("(none)")
	}
	ub.WriteString("\n\nTOOL_IDS (choose only from this list):\n")
	ub.WriteString(toolBlock)

	maxTok := r.cfg.LocalIntel.SmartRoutingMaxTokens
	if maxTok <= 0 {
		maxTok = defaultSmartRoutingMaxTokens
	}
	out, err := eng.Complete(ctx, localintel.Request{
		System:    smartRoutingSystem,
		User:      ub.String(),
		MaxTokens: maxTok,
	})
	if err != nil {
		r.log.Warn("local_intel: smart routing completion failed", zap.Error(err))
		return
	}
	tools, focus := parseLocalIntelRoutingOutput(out)
	if len(tools) == 0 && strings.TrimSpace(focus) == "" {
		r.log.Debug("local_intel: smart routing produced no parseable hints")
		return
	}
	allowed := make(map[string]struct{}, len(names))
	for _, n := range names {
		allowed[n] = struct{}{}
	}
	var validated []string
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := allowed[t]; !ok {
			continue
		}
		validated = append(validated, t)
		if len(validated) >= 12 {
			break
		}
	}
	r.localIntelRoutingTools = validated
	r.localIntelRoutingSkillFocus = strings.TrimSpace(focus)
	if len(r.localIntelRoutingTools) > 0 {
		r.log.Debug("local_intel: smart routing tools", zap.Strings("tools", r.localIntelRoutingTools))
	}
}

func (r *Runner) toolNamesForRouting() []string {
	if r.tools == nil {
		return nil
	}
	defs := r.tools.Definitions()
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		n := strings.TrimSpace(d.Name)
		if n != "" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func parseLocalIntelRoutingOutput(s string) (tools []string, skillFocus string) {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(line[:idx]))
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, "`\"'")
		switch key {
		case "tools":
			for _, p := range strings.Split(val, ",") {
				p = strings.TrimSpace(strings.Trim(p, "`\"'"))
				if p != "" {
					tools = append(tools, p)
				}
			}
		case "skills_focus", "skills":
			if skillFocus == "" {
				skillFocus = val
			}
		}
	}
	if skillFocus != "" {
		skillFocus = truncateUTF8(strings.TrimSpace(skillFocus), 480)
	}
	return tools, skillFocus
}
