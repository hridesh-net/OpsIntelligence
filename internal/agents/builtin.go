package agents

// devOpsAgent handles PR reviews, CI/CD pipelines, and DevOps platform queries.
func devOpsAgent() AgentDef {
	return AgentDef{
		Name:        "devops",
		Description: "Specialist for PR reviews, CI/CD pipelines, GitHub/GitLab/Jenkins/SonarQube, and DevOps workflows.",
		SystemPromptFocus: `## DevOps Specialist Mode
You are operating as the DevOps specialist agent. Your focus:
- PR review and analysis (devops.github.*, devops.gitlab.*)
- CI/CD pipeline status and triage (devops.jenkins.*, pipeline tools)
- SonarQube quality gate analysis (devops.sonar.*)
- Repository workflow automation

Prioritize devops.* tools. Use chain_run pr-review for comprehensive reviews.
Stay read-only unless explicitly instructed to post reviews or comments.`,
		Keywords: []string{
			"pull request", "pr review", "pr #", "merge request", "mr review",
			"pipeline", "ci/cd", "github", "gitlab", "jenkins", "sonarqube",
			"workflow", "branch", "diff", "code review", "deploy", "build",
			"failed build", "ci check", "quality gate", "sonar", "coverage",
		},
		AllowedTools: []string{
			"bash", "read_file", "write_file", "grep",
			"devops.github.get_pr", "devops.github.list_prs", "devops.github.review_pr",
			"devops.github.submit_review", "devops.github.get_workflow_run",
			"devops.gitlab.get_mr", "devops.gitlab.list_mrs",
			"devops.jenkins.get_job", "devops.jenkins.list_builds",
			"devops.sonar.get_quality_gate", "devops.sonar.list_issues",
			"devops.diagnose", "chain_run", "chain_list",
			"memory_search", "fact_check", "web_search", "find_tools",
		},
		BlockedTools: []string{
			"subagent_run", "subagent_run_parallel", "subagent_run_async",
		},
	}
}

// securityAgent handles vulnerability scanning, audit review, and compliance.
func securityAgent() AgentDef {
	return AgentDef{
		Name:        "security",
		Description: "Specialist for CVE scanning, security audit review, compliance checks, and policy validation.",
		SystemPromptFocus: `## Security Specialist Mode
You are operating as the security specialist agent. Your focus:
- CVE and vulnerability analysis
- Security audit log review and compliance checks
- Code security scanning (SAST, secret detection)
- Policy and guardrail validation

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
		},
		BlockedTools: []string{
			"subagent_run", "subagent_run_parallel", "subagent_run_async",
			"write_file", "edit", "apply_patch",
		},
	}
}

// repoIntelAgent handles repository intelligence, indexing, and semantic search.
func repoIntelAgent() AgentDef {
	return AgentDef{
		Name:        "repointel",
		Description: "Specialist for repository intelligence: code indexing, semantic search, call graph analysis, and bottleneck detection.",
		SystemPromptFocus: `## Repository Intelligence Specialist Mode
You are operating as the repository intelligence specialist agent. Your focus:
- Repository indexing and full-tree analysis
- Semantic code search and RAG over indexed repositories
- Call graph and dependency analysis
- Code bottleneck and technical debt scanning

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
	}
}
