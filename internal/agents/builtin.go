package agents

import (
	"bytes"
	"os"
	"strings"
)

// ── Built-in agents ───────────────────────────────────────────────────────────
//
// Every agent's ContextLoader injects dynamic context at spawn time in this order:
//   1. Execution pipeline  — from <agentsConfigDir>/<name>/flow.yaml (or flow.md)
//   2. Domain-specific ctx — workflow policy / security policies / repo summaries
//
// The pipeline is always first so the agent sees stage instructions before
// any other injected content.

func newDevOpsAgent(opts RegistryOpts) AgentDef {
	return AgentDef{
		Name: "devops",
		Description: "Specialist for CI/CD pipelines, GitHub Actions, GitLab pipelines, " +
			"Jenkins job status, and SonarQube quality gates. Delegates PR/MR code review to the pr_review agent.",
		SystemPromptFocus: `## DevOps Specialist Mode
You are operating as the DevOps specialist agent. Your focus:
- CI/CD pipeline status and triage (devops.jenkins.*, devops.gitlab.pipelines)
- GitHub Actions workflow runs and commit statuses (devops.github.workflow_runs, devops.github.commit_status)
- SonarQube quality gate analysis (devops.sonar.*)
- Multi-cloud read posture: inventory, cost summaries, audit-style events (devops.cloud.*) when configured
- Repository workflow automation and deployment diagnostics

If a configured workflow policy is injected at the top of this prompt, follow it strictly.
For pull-request / code review requests, delegate to the pr_review specialist.
Stay read-only unless explicitly instructed otherwise.`,
		Keywords: []string{
			"pipeline", "ci/cd", "jenkins", "sonarqube",
			"workflow", "deploy", "build", "failed build",
			"ci check", "quality gate", "sonar", "coverage",
			"github actions", "gitlab pipeline", "commit status",
			"aws", "azure", "gcp", "cloud cost", "cloud inventory", "cloudtrail", "activity log",
		},
		AllowedTools: []string{
			"bash", "read_file", "grep",
			"devops.github.workflow_runs", "devops.github.commit_status",
			"devops.github.list_prs",
			"devops.gitlab.list_mrs", "devops.gitlab.pipelines",
			"devops.jenkins.job_status", "devops.jenkins.list_builds",
			"devops.sonar.quality_gate", "devops.sonar.issues",
			"devops.diagnose", "chain_run", "chain_list",
			"memory_search", "fact_check", "web_search", "find_tools",
			"devops.workflow.get",
			"devops.cloud.inventory", "devops.cloud.cost_summary", "devops.cloud.audit_events",
		},
		BlockedTools: []string{
			"subagent_run", "subagent_run_parallel", "subagent_run_async",
		},
		ContextLoader: func() string {
			var sb strings.Builder
			if s := loadFlowContext("devops", opts.AgentsConfigDir, opts.FlowEvalCtx); s != "" {
				sb.WriteString(s)
			}
			if s := readFileContext(opts.DevOpsWorkflowPath, "## Configured DevOps Workflow Policy"); s != "" {
				sb.WriteString(s)
			}
			return sb.String()
		},
	}
}

func newSecurityAgent(opts RegistryOpts) AgentDef {
	return AgentDef{
		Name: "security",
		Description: "Specialist for CVE scanning, security audit review, compliance checks, " +
			"and policy validation against POLICIES.md and team policies.",
		SystemPromptFocus: `## Security Specialist Mode
You are operating as the security specialist agent. Your focus:
- CVE and vulnerability analysis
- Security audit log review and compliance checks
- Code security scanning (SAST, secret detection)
- Policy and guardrail validation

Active security policies are injected at the top of this prompt — apply them strictly.
Be thorough and conservative. Flag anything suspicious. Never suppress findings.
Prefer read-only operations; escalate write actions to the master agent.`,
		Keywords: []string{
			"cve", "vulnerability", "vulnerabilit", "security", "exploit",
			"audit", "compliance", "sast", "secret", "credential", "leak",
			"xss", "sql injection", "injection", "owasp", "cvss",
			"security scan", "pentest", "hardening", "firewall", "auth bypass",
		},
		AllowedTools: []string{
			"bash", "read_file", "grep",
			"memory_search", "fact_check", "web_search", "find_tools",
			"devops.diagnose",
			"security.policy.get",
		},
		BlockedTools: []string{
			"subagent_run", "subagent_run_parallel", "subagent_run_async",
			"write_file", "edit", "apply_patch",
		},
		ContextLoader: func() string {
			var sb strings.Builder
			if s := loadFlowContext("security", opts.AgentsConfigDir, opts.FlowEvalCtx); s != "" {
				sb.WriteString(s)
			}
			if opts.SecurityPolicyFn != nil {
				if pol := opts.SecurityPolicyFn(); strings.TrimSpace(pol) != "" {
					sb.WriteString("## Active Security Policies\n")
					sb.WriteString(pol)
					sb.WriteString("\n\n")
				}
			}
			return sb.String()
		},
	}
}

func newRepoIntelAgent(opts RegistryOpts) AgentDef {
	return AgentDef{
		Name: "repointel",
		Description: "Specialist for repository intelligence: code indexing, semantic search, " +
			"call graph analysis, and bottleneck detection.",
		SystemPromptFocus: `## Repository Intelligence Specialist Mode
You are operating as the repository intelligence specialist agent. Your focus:
- Repository indexing and full-tree analysis
- Semantic code search and RAG over indexed repositories
- Call graph and dependency analysis
- Code bottleneck and technical debt scanning

Indexed repository summaries are injected at the top of this prompt when available.
Use repo_intel tools and memory_search for context. Provide specific,
evidence-backed findings with file paths and line references.`,
		Keywords: []string{
			"repo", "repository", "codebase", "index", "indexing",
			"call graph", "dependency", "bottleneck", "technical debt",
			"semantic search", "code search", "scan", "symbol",
			"file structure", "architecture overview", "code quality",
		},
		AllowedTools: []string{
			"bash", "read_file", "grep", "list_dir",
			"memory_search", "fact_check", "web_search", "find_tools",
			"devops.diagnose",
		},
		BlockedTools: []string{
			"subagent_run", "subagent_run_parallel", "subagent_run_async",
			"write_file", "edit", "apply_patch",
		},
		ContextLoader: func() string {
			var sb strings.Builder
			if s := loadFlowContext("repointel", opts.AgentsConfigDir, opts.FlowEvalCtx); s != "" {
				sb.WriteString(s)
			}
			if opts.RepoSummaryFn != nil {
				if sum := opts.RepoSummaryFn(); sum != "" {
					sb.WriteString("## Indexed Repository Intelligence\n")
					sb.WriteString(sum)
					sb.WriteString("\n\n")
				}
			}
			return sb.String()
		},
	}
}

// ── PR Review agent ───────────────────────────────────────────────────────────

// PRReviewOpts configures the PR review specialist at registry-build time.
type PRReviewOpts struct {
	// MethodologyPath is the absolute path to methodology.md.
	MethodologyPath string

	// RepoSummaryFn returns a compact repo intelligence summary in markdown.
	RepoSummaryFn func() string

	// AgentsConfigDir is the root for per-agent flow files.
	AgentsConfigDir string

	// FlowEvalCtx carries integration flags for stage condition evaluation.
	FlowEvalCtx FlowEvalContext
}

// NewPRReviewAgent returns an AgentDef for the dedicated PR review specialist.
func NewPRReviewAgent(opts PRReviewOpts) AgentDef {
	return AgentDef{
		Name: "pr_review",
		Description: "Dedicated specialist for code review on GitHub PRs and GitLab MRs: " +
			"fetches diffs, executes configurable review stages, applies review methodology, " +
			"checks security/quality scans, and posts formal inline reviews.",
		SystemPromptFocus: `## PR Review Specialist Mode
You are the dedicated pull-request review specialist. When asked to review a GitHub PR,
**posting a formal review with inline comments IS the expected outcome** — never just paste
the findings as chat prose. "Review this PR" means review AND post.

**Primary action — do this by default for any GitHub PR review request:**
Call ` + "`devops.github.review_pr`" + ` with the PR URL, e.g.
  devops.github.review_pr {"pr_url": "https://github.com/owner/repo/pull/42"}
This single atomic tool fetches the PR metadata and unified diff, analyses it for
severity-graded findings with exact file:line anchors, and submits a formal GitHub review
with inline line-level comments and suggested fixes — in one call. Pass ` + "`focus`" + ` when the
user asks for a specific lens (security, performance, correctness). When it returns, report
the posted review URL and a one-line summary of what was flagged.

Only pass ` + "`dry_run: true`" + ` (or stay read-only) when the user EXPLICITLY asks to preview
without posting — e.g. "just summarize", "don't comment", "dry run". Otherwise, post.

**GitLab MRs / multi-stage pipelines:** if an Execution Pipeline is injected above, follow its
stages in order; otherwise use: linter & style → logic & bottlenecks → CVE/security scan →
CI status → final verdict & post. You may enrich a review with the configured methodology
(pr_review.methodology.get), SonarQube (devops.sonar.*) and episodic memory (memory_search),
but these are optional and must not block posting the review.

**Findings severity guide**:
  critical / high  → REQUEST_CHANGES
  medium           → COMMENT
  nit              → COMMENT only when numerous

Never approve without checking available scans.`,
		Keywords: []string{
			"pull request", "pr review", "pr #", "review pr",
			"merge request", "mr review", "review mr",
			"code review", "review this pr", "review this mr",
			"diff", "inline comment", "approve pr", "request changes",
			"lgtm", "looks good to me", "nit", "review finding",
			"annotate pr", "check pr", "analyze pr",
		},
		AllowedTools: []string{
			// Local read access for context around changed files
			"bash", "read_file", "grep",
			// GitHub PR: read, diff, file content, existing reviews, CI
			"devops.github.list_prs",
			"devops.github.pull_request",
			"devops.github.pr_diff",
			"devops.github.get_file",
			"devops.github.list_reviews",
			"devops.github.pr_comment",
			"devops.github.submit_review",
			"devops.github.review_pr",
			"devops.github.workflow_runs",
			"devops.github.commit_status",
			"devops.github.check_runs",
			// GitLab MR + CI
			"devops.gitlab.list_mrs",
			"devops.gitlab.pipelines",
			// Jenkins CI status
			"devops.jenkins.job_status",
			// Quality & security scanning
			"devops.sonar.quality_gate",
			"devops.sonar.issues",
			"security.policy.get",
			// CI sandbox execution
			"devops.pipeline.run_sandbox",
			// PR review task management
			"pr_review_tasks", "pr_review_cancel", "pr_review_events",
			// Prompt chain execution
			"chain_run", "chain_list",
			// Pipeline / flow introspection
			"agent.flow.get",
			// Intelligence & research
			"memory_search", "fact_check", "web_search", "find_tools",
			"devops.diagnose",
			// Methodology management
			"pr_review.methodology.get", "pr_review.methodology.set",
		},
		BlockedTools: []string{
			"subagent_run", "subagent_run_parallel", "subagent_run_async",
			"write_file", "edit", "apply_patch",
		},
		ContextLoader: func() string {
			var sb strings.Builder
			// Pipeline stages first — so the agent sees them before methodology.
			if s := loadFlowContext("pr_review", opts.AgentsConfigDir, opts.FlowEvalCtx); s != "" {
				sb.WriteString(s)
			}
			// Review methodology.
			if opts.MethodologyPath != "" {
				if data, err := os.ReadFile(opts.MethodologyPath); err == nil && len(bytes.TrimSpace(data)) > 0 {
					sb.WriteString("## Configured Review Methodology\n")
					sb.Write(data)
					sb.WriteString("\n\n")
				}
			}
			// Repo intelligence.
			if opts.RepoSummaryFn != nil {
				if sum := opts.RepoSummaryFn(); sum != "" {
					sb.WriteString("## Repository Intelligence\n")
					sb.WriteString(sum)
					sb.WriteString("\n\n")
				}
			}
			return sb.String()
		},
	}
}

// ── Context loader helpers ────────────────────────────────────────────────────

// readFileContext reads a markdown file and prepends it under heading.
// Returns empty string when path is empty or the file doesn't exist / is blank.
func readFileContext(path, heading string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return ""
	}
	return heading + "\n" + string(data) + "\n\n"
}
