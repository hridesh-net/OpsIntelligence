package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/github"
	"github.com/opsintelligence/opsintelligence/internal/devops/gitlab"
	"github.com/opsintelligence/opsintelligence/internal/devops/jenkins"
	"github.com/opsintelligence/opsintelligence/internal/devops/sonar"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

type diagnoseTool struct {
	cfg config.DevOpsConfig
}

func (t *diagnoseTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.diagnose",
		Description: "Diagnose the health of all DevOps integrations (GitHub, GitLab, Jenkins, Sonar). Returns connectivity status, credential validity, and fix suggestions if something is broken.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"check_network": map[string]any{"type": "boolean", "description": "If true, performs a live ping to each service. Default true.", "default": true},
			},
		},
	}
}

func (t *diagnoseTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		CheckNetwork bool `json:"check_network"`
	}
	a.CheckNetwork = true
	_ = json.Unmarshal(input, &a)

	var sb strings.Builder
	sb.WriteString("## DevOps Diagnostic Report\n\n")

	httpc := &http.Client{Timeout: 10 * time.Second}

	// GitHub
	sb.WriteString("### GitHub\n")
	if !t.cfg.GitHub.Enabled {
		sb.WriteString("- Status: [DISABLED]\n- Reason: `devops.github.enabled` is false in config.\n")
	} else if t.cfg.GitHub.Token == "" {
		sb.WriteString("- Status: [BROKEN]\n- Reason: GitHub token is empty.\n- Fix: Set `GITHUB_TOKEN` environment variable or `devops.github.token` in config.\n")
	} else if a.CheckNetwork {
		gh := github.New(github.Config{Token: t.cfg.GitHub.Token, BaseURL: t.cfg.GitHub.BaseURL, DefaultOrg: t.cfg.GitHub.DefaultOrg}, httpc)
		if err := gh.Ping(ctx); err != nil {
			sb.WriteString(fmt.Sprintf("- Status: [UNREACHABLE]\n- Error: %v\n", err))
		} else {
			sb.WriteString("- Status: [OK]\n- Message: Successfully reached GitHub API.\n")
		}
	} else {
		sb.WriteString("- Status: [CONFIGURED]\n")
	}

	// GitLab
	sb.WriteString("\n### GitLab\n")
	if !t.cfg.GitLab.Enabled {
		sb.WriteString("- Status: [DISABLED]\n")
	} else if t.cfg.GitLab.Token == "" || t.cfg.GitLab.BaseURL == "" {
		sb.WriteString("- Status: [BROKEN]\n- Reason: Token or BaseURL is empty.\n")
	} else if a.CheckNetwork {
		gl := gitlab.New(gitlab.Config{BaseURL: t.cfg.GitLab.BaseURL, Token: t.cfg.GitLab.Token}, httpc)
		if err := gl.Ping(ctx); err != nil {
			sb.WriteString(fmt.Sprintf("- Status: [UNREACHABLE]\n- Error: %v\n", err))
		} else {
			sb.WriteString("- Status: [OK]\n")
		}
	} else {
		sb.WriteString("- Status: [CONFIGURED]\n")
	}

	// Jenkins
	sb.WriteString("\n### Jenkins\n")
	if !t.cfg.Jenkins.Enabled {
		sb.WriteString("- Status: [DISABLED]\n")
	} else if t.cfg.Jenkins.Token == "" || t.cfg.Jenkins.BaseURL == "" {
		sb.WriteString("- Status: [BROKEN]\n")
	} else if a.CheckNetwork {
		jk := jenkins.New(jenkins.Config{BaseURL: t.cfg.Jenkins.BaseURL, User: t.cfg.Jenkins.User, Token: t.cfg.Jenkins.Token}, httpc)
		if err := jk.Ping(ctx); err != nil {
			sb.WriteString(fmt.Sprintf("- Status: [UNREACHABLE]\n- Error: %v\n", err))
		} else {
			sb.WriteString("- Status: [OK]\n")
		}
	} else {
		sb.WriteString("- Status: [CONFIGURED]\n")
	}

	// Sonar
	sb.WriteString("\n### Sonar\n")
	if !t.cfg.Sonar.Enabled {
		sb.WriteString("- Status: [DISABLED]\n")
	} else if t.cfg.Sonar.Token == "" || t.cfg.Sonar.BaseURL == "" {
		sb.WriteString("- Status: [BROKEN]\n")
	} else if a.CheckNetwork {
		sn := sonar.New(sonar.Config{BaseURL: t.cfg.Sonar.BaseURL, Token: t.cfg.Sonar.Token, ProjectKeyPrefix: t.cfg.Sonar.ProjectKeyPrefix}, httpc)
		if err := sn.Ping(ctx); err != nil {
			sb.WriteString(fmt.Sprintf("- Status: [UNREACHABLE]\n- Error: %v\n", err))
		} else {
			sb.WriteString("- Status: [OK]\n")
		}
	} else {
		sb.WriteString("- Status: [CONFIGURED]\n")
	}

	return sb.String(), nil
}
