package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// GitHubSetupGuidePlain is the same content as RenderGitHubSetupGuide without ANSI styles.
func GitHubSetupGuidePlain() string {
	var b strings.Builder
	b.WriteString("GitHub + OpsIntelligence — where credentials go\n\n")
	b.WriteString("1) devops.github (Dashboard Settings → DevOps, or opsintelligence.yaml)\n")
	b.WriteString("   Personal Access Token (PAT) for the GitHub REST API.\n")
	b.WriteString("   Used by devops.github.pull_request, pr_diff, Actions, etc.\n")
	b.WriteString("   Set token OR token_env (e.g. OPSINTEL_GITHUB_TOKEN). Scopes: typically\n")
	b.WriteString("   \"repo\" for private repos; public-only can use narrower scopes.\n\n")
	b.WriteString("2) Webhooks → GitHub adapter (Settings → Webhooks)\n")
	b.WriteString("   HMAC secret so GitHub can sign deliveries (X-Hub-Signature-256).\n")
	b.WriteString("   NOT the same value as the PAT. Maps to webhooks.adapters.github.secret\n")
	b.WriteString("   and env OPSINTEL_GITHUB_WEBHOOK_SECRET. URL path: /api/webhook/github\n\n")
	b.WriteString("3) Posting PR reviews back (optional, CodeRabbit-style)\n")
	b.WriteString("   Needs GitHub CLI: gh, plus GH_TOKEN or a PAT with permission to create\n")
	b.WriteString("   pull request reviews. Install skill: opsintelligence skills install gh-pr-review\n")
	b.WriteString("   Reference: doc/github-webhooks.md\n\n")
	b.WriteString("4) Verify\n")
	b.WriteString("   opsintelligence doctor        (config + API reachability)\n")
	b.WriteString("   opsintelligence guides github (this cheat sheet)\n")
	return b.String()
}

// RenderGitHubSetupGuide prints a TUI-styled cheat sheet for GitHub-related config.
func RenderGitHubSetupGuide() string {
	sections := []struct {
		title string
		lines []string
	}{
		{
			title: "1. DevOps → GitHub (REST tools)",
			lines: []string{
				"PAT for devops.github.* — PR metadata, diff, Actions, checks.",
				"Configure token or token_env (e.g. OPSINTEL_GITHUB_TOKEN).",
				"Private repos: usually need \"repo\" scope on the PAT.",
			},
		},
		{
			title: "2. Webhooks → GitHub adapter (ingress)",
			lines: []string{
				"HMAC secret proves payloads came from GitHub — not your PAT.",
				"YAML: webhooks.adapters.github.secret + OPSINTEL_GITHUB_WEBHOOK_SECRET.",
				"GitHub posts to: https://<host>/api/webhook/github",
			},
		},
		{
			title: "3. PR reviews posted to GitHub (gh)",
			lines: []string{
				"Install gh; auth with GH_TOKEN / PAT allowed to create reviews.",
				"opsintelligence skills install gh-pr-review",
				"See doc/github-webhooks.md (CodeRabbit-style flow).",
			},
		},
		{
			title: "4. Where to edit",
			lines: []string{
				"Web UI: Dashboard → Settings → DevOps / Webhooks.",
				"CLI: opsintelligence onboard  or  edit ~/.opsintelligence/opsintelligence.yaml",
			},
		},
	}

	var blocks []string
	for _, sec := range sections {
		head := Primary.Render("▸ " + sec.title)
		var body strings.Builder
		for _, ln := range sec.lines {
			body.WriteString(Muted.Render("   "+ln) + "\n")
		}
		blocks = append(blocks, head+"\n"+body.String())
	}

	title := Header.Render("GitHub setup — which credential for which action")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Background(ColorSurface).
		Padding(1, 2).
		Width(78).
		Render(title + "\n\n" + strings.Join(blocks, "\n"))

	footer := "\n" + Muted.Render("Tip: run any time — opsintelligence guides github") + "\n"
	return "\n" + box + footer
}
