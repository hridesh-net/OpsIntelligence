package main

// onboard_summary_print.go — plain-text summary printer shown at the end of
// `opsintelligence onboard`. Replaces the legacy tabbed bubbletea view
// (cmd/opsintelligence/tui/onboard_summary.go) removed in Phase 5d. Users
// who want a live, searchable, tabbed view of the merged configuration can
// run `opsintelligence status` (which is the Rust dashboard view).

import (
	"fmt"
	"strings"
)

func printOnboardSummary(s *onboardState, tailscalePublicURL string) {
	fmt.Println()
	fmt.Println("✓ Onboarding complete — configuration saved to " + s.configPath)
	fmt.Println()

	section := func(title string) {
		fmt.Println("── " + title + " " + strings.Repeat("─", max(0, 50-len(title))))
	}
	row := func(k, v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			v = "—"
		}
		fmt.Printf("  %-22s %s\n", k, v)
	}
	rowBool := func(k string, b bool) {
		if b {
			row(k, "true")
		} else {
			row(k, "false")
		}
	}

	section("Provider")
	row("primary", s.primary.provider)
	row("primary.model", s.primary.model)
	if s.secChoice == "configure" && s.secondary.provider != "" && s.secondary.provider != "none" {
		row("secondary", s.secondary.provider)
	}
	row("embed.provider", s.embed.provider)
	row("embed.model", s.embed.model)
	fmt.Println()

	section("Gateway & Access")
	gwMode := normalizeGatewayBind(s.gwMode)
	row("bind", fmt.Sprintf("%s:%d (%s)", s.gwHost, s.gwPort, gwMode))
	if tailscalePublicURL != "" {
		row("tailscale.public", tailscalePublicURL)
	}
	rowBool("plano.enabled", s.usePlano)
	if s.usePlano {
		row("plano.endpoint", s.planoEndpoint)
	}
	rowBool("local_intel", s.localIntelEnabled)
	if s.localIntelEnabled && s.localIntelGGUF != "" {
		row("local_intel.gguf", s.localIntelGGUF)
	}
	rowBool("mempalace", s.memPalaceEnabled)
	fmt.Println()

	if len(s.selectedChannels) > 0 {
		section("Channels")
		row("enabled", strings.Join(s.selectedChannels, ", "))
		fmt.Println()
	}

	devops := []string{}
	if s.githubToken != "" || strings.TrimSpace(s.githubTokenEnv) != "" {
		devops = append(devops, "github")
	}
	if strings.TrimSpace(s.gitlabURL) != "" && (s.gitlabToken != "" || strings.TrimSpace(s.gitlabTokenEnv) != "") {
		devops = append(devops, "gitlab")
	}
	if strings.TrimSpace(s.jenkinsURL) != "" && (s.jenkinsToken != "" || strings.TrimSpace(s.jenkinsTokenEnv) != "") {
		devops = append(devops, "jenkins")
	}
	if strings.TrimSpace(s.sonarURL) != "" && (s.sonarToken != "" || strings.TrimSpace(s.sonarTokenEnv) != "") {
		devops = append(devops, "sonar")
	}
	if len(devops) > 0 {
		section("DevOps Integrations")
		row("enabled", strings.Join(devops, ", "))
		if s.githubDefaultOrg != "" {
			row("github.org", s.githubDefaultOrg)
		}
		if s.gitlabURL != "" {
			row("gitlab.url", s.gitlabURL)
		}
		if s.jenkinsURL != "" {
			row("jenkins.url", s.jenkinsURL)
		}
		if s.sonarURL != "" {
			row("sonar.url", s.sonarURL)
		}
		fmt.Println()
	}

	if len(s.selectedSkills) > 0 {
		section("Skills")
		row("enabled", fmt.Sprintf("%d installed", len(s.selectedSkills)))
		fmt.Println()
	}

	fmt.Println("Next:  opsintelligence status      # live tabbed view of the merged config")
	fmt.Println("       opsintelligence doctor      # verify everything is reachable")
	fmt.Println("       opsintelligence agent       # start an interactive REPL")
	fmt.Println()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
