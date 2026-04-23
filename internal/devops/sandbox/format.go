package sandbox

import (
	"fmt"
	"strings"
)

const maxLLMOutputBytes = 1500
const maxOutputBytes = 8 * 1024

// FormatForLLM returns a compact CI result block for injection into the LLM
// analysis prompt. Kept under 2,000 characters to preserve context budget.
func FormatForLLM(r *Result) string {
	if r == nil || r.Skipped {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("### CI Pipeline Results\n")
	sb.WriteString(fmt.Sprintf("CI System: %s\n", r.CIKind))
	if r.Succeeded {
		sb.WriteString("Status: PASSED\n")
	} else {
		sb.WriteString("Status: FAILED\n")
	}
	if len(r.Errors) > 0 {
		sb.WriteString("Errors detected:\n")
		for _, e := range r.Errors {
			sb.WriteString("- " + e + "\n")
		}
	}
	if r.Output != "" {
		snippet := r.Output
		if len(snippet) > maxLLMOutputBytes {
			snippet = snippet[:maxLLMOutputBytes] + "\n[truncated]"
		}
		sb.WriteString("\nOutput:\n```\n")
		sb.WriteString(snippet)
		sb.WriteString("\n```\n")
	}
	return sb.String()
}

// FormatForReviewBody returns a GitHub-Markdown section for the PR review body.
// Full pipeline output is wrapped in a collapsible <details> block.
func FormatForReviewBody(r *Result) string {
	if r == nil || r.Skipped {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n---\n### CI Pipeline Sandbox\n")

	ciLabel := ciKindLabel(r.CIKind)
	sb.WriteString(fmt.Sprintf("**CI System:** %s  \n", ciLabel))

	if r.Succeeded {
		sb.WriteString(fmt.Sprintf("**Result:** ✅ PASSED (ran in %ds)\n", r.ElapsedSecs))
	} else {
		sb.WriteString(fmt.Sprintf("**Result:** ❌ FAILED (ran in %ds)\n", r.ElapsedSecs))
	}

	if len(r.Errors) > 0 {
		sb.WriteString("\nErrors found:\n")
		for _, e := range r.Errors {
			sb.WriteString(fmt.Sprintf("- `%s`\n", e))
		}
	}

	if r.Output != "" {
		output := r.Output
		if len(output) > maxOutputBytes {
			output = output[:maxOutputBytes] + "\n[output truncated at 8KB]"
		}
		sb.WriteString("\n<details><summary>Full pipeline output</summary>\n\n```\n")
		sb.WriteString(output)
		sb.WriteString("\n```\n\n</details>\n")
	}

	return sb.String()
}

func ciKindLabel(k CIKind) string {
	switch k {
	case CIKindGitHubActions:
		return "GitHub Actions"
	case CIKindGitLabCI:
		return "GitLab CI"
	case CIKindCircleCI:
		return "CircleCI"
	default:
		return string(k)
	}
}
