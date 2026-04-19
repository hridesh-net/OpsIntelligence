package agent

import (
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// normalizeToolNameLikeBedrock maps non [a-zA-Z0-9_-] runes to '_' (same rule as
// provider/bedrock) so we can match Bedrock Converse tool names back to catalog names.
func normalizeToolNameLikeBedrock(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := strings.Trim(b.String(), "_-")
	if s == "" {
		return "tool"
	}
	return s
}

// resolveCatalogToolName maps a model-supplied or Bedrock-alias tool name to the
// registry's canonical name when Get(name) misses (e.g. devops_github_list_prs → devops.github.list_prs).
func (r *Runner) resolveCatalogToolName(name string) string {
	if name == "" {
		return name
	}
	if _, ok := r.tools.Get(name); ok {
		return name
	}
	target := normalizeToolNameLikeBedrock(name)
	for _, def := range r.tools.Definitions() {
		if normalizeToolNameLikeBedrock(def.Name) == target {
			return def.Name
		}
	}
	return name
}

func (r *Runner) normalizeToolCallNames(parts []provider.ContentPart) {
	for i := range parts {
		if parts[i].Type == provider.ContentTypeToolUse && parts[i].ToolName != "" {
			parts[i].ToolName = r.resolveCatalogToolName(parts[i].ToolName)
		}
	}
}
