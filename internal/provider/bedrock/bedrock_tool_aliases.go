package bedrock

import (
	"fmt"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// bedrockToolAliases maps registry tool names to/from AWS Converse tool names.
// Converse requires each tool name to match [a-zA-Z0-9_-]+ (no dots, etc.).
type bedrockToolAliases struct {
	toAWS   map[string]string
	fromAWS map[string]string
}

func newBedrockToolAliases(tools []provider.ToolDef) *bedrockToolAliases {
	a := &bedrockToolAliases{
		toAWS:   make(map[string]string),
		fromAWS: make(map[string]string),
	}
	if len(tools) == 0 {
		return a
	}
	used := make(map[string]struct{})
	for _, t := range tools {
		canonical := t.Name
		base := bedrockNormalizeToolName(canonical)
		name := base
		for n := 2; ; n++ {
			if _, ok := used[name]; !ok {
				break
			}
			name = fmt.Sprintf("%s_%d", base, n)
		}
		used[name] = struct{}{}
		a.toAWS[canonical] = name
		a.fromAWS[name] = canonical
	}
	return a
}

func bedrockNormalizeToolName(name string) string {
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

func (a *bedrockToolAliases) toAWSName(canonical string) string {
	if a != nil {
		if v, ok := a.toAWS[canonical]; ok {
			return v
		}
	}
	return bedrockNormalizeToolName(canonical)
}

func (a *bedrockToolAliases) fromAWSName(awsName string) string {
	if a != nil {
		if v, ok := a.fromAWS[awsName]; ok {
			return v
		}
	}
	return awsName
}
