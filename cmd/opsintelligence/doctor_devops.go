package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/github"
	"github.com/opsintelligence/opsintelligence/internal/devops/gitlab"
	"github.com/opsintelligence/opsintelligence/internal/devops/jenkins"
	"github.com/opsintelligence/opsintelligence/internal/devops/sonar"
)

// runDoctorDevOpsChecks returns reachability checks for every enabled
// DevOps integration. Each integration gets a short, bounded Ping; failure
// is reported as an error check with a sanitized message.
func runDoctorDevOpsChecks(ctx context.Context, cfg *config.Config, skipNetwork bool, perTimeout time.Duration) []doctorCheck {
	if cfg == nil {
		return nil
	}
	if skipNetwork {
		return []doctorCheck{{
			ID:       "devops.network",
			Severity: "skipped",
			Message:  "DevOps reachability checks skipped (--skip-network).",
		}}
	}
	if perTimeout <= 0 {
		perTimeout = 10 * time.Second
	}
	var out []doctorCheck
	httpClient := &http.Client{Timeout: perTimeout}

	if cfg.DevOps.GitHub.Enabled {
		out = append(out, pingGitHub(ctx, cfg.DevOps.GitHub, httpClient, perTimeout))
	} else {
		out = append(out, doctorCheck{ID: "devops.github", Severity: "skipped", Message: "devops.github.enabled is false."})
	}
	if cfg.DevOps.GitLab.Enabled {
		out = append(out, pingGitLab(ctx, cfg.DevOps.GitLab, httpClient, perTimeout))
	} else {
		out = append(out, doctorCheck{ID: "devops.gitlab", Severity: "skipped", Message: "devops.gitlab.enabled is false."})
	}
	if cfg.DevOps.Jenkins.Enabled {
		out = append(out, pingJenkins(ctx, cfg.DevOps.Jenkins, httpClient, perTimeout))
	} else {
		out = append(out, doctorCheck{ID: "devops.jenkins", Severity: "skipped", Message: "devops.jenkins.enabled is false."})
	}
	if cfg.DevOps.Sonar.Enabled {
		out = append(out, pingSonar(ctx, cfg.DevOps.Sonar, httpClient, perTimeout))
	} else {
		out = append(out, doctorCheck{ID: "devops.sonar", Severity: "skipped", Message: "devops.sonar.enabled is false."})
	}
	return out
}

func pingGitHub(ctx context.Context, cfg config.GitHubConfig, httpc *http.Client, timeout time.Duration) doctorCheck {
	if strings.TrimSpace(cfg.Token) == "" {
		return doctorCheck{ID: "devops.github", Severity: "error", Message: "GitHub enabled but no token (set token or token_env)."}
	}
	c := github.New(github.Config{Token: cfg.Token, BaseURL: cfg.BaseURL, DefaultOrg: cfg.DefaultOrg}, httpc)
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.Ping(pctx); err != nil {
		return doctorCheck{ID: "devops.github", Severity: "error", Message: sanitizeDoctorMessage(fmt.Sprintf("GET /rate_limit: %v", err))}
	}
	return doctorCheck{ID: "devops.github", Severity: "ok", Message: "GitHub: rate_limit reachable with provided token."}
}

func pingGitLab(ctx context.Context, cfg config.GitLabConfig, httpc *http.Client, timeout time.Duration) doctorCheck {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return doctorCheck{ID: "devops.gitlab", Severity: "error", Message: "GitLab enabled but base_url is empty."}
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return doctorCheck{ID: "devops.gitlab", Severity: "error", Message: "GitLab enabled but no token."}
	}
	c := gitlab.New(gitlab.Config{BaseURL: cfg.BaseURL, Token: cfg.Token}, httpc)
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.Ping(pctx); err != nil {
		return doctorCheck{ID: "devops.gitlab", Severity: "error", Message: sanitizeDoctorMessage(fmt.Sprintf("GET /api/v4/version: %v", err))}
	}
	return doctorCheck{ID: "devops.gitlab", Severity: "ok", Message: "GitLab: /api/v4/version reachable with provided token."}
}

func pingJenkins(ctx context.Context, cfg config.JenkinsConfig, httpc *http.Client, timeout time.Duration) doctorCheck {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return doctorCheck{ID: "devops.jenkins", Severity: "error", Message: "Jenkins enabled but base_url is empty."}
	}
	if cfg.User == "" || cfg.Token == "" {
		return doctorCheck{ID: "devops.jenkins", Severity: "error", Message: "Jenkins enabled but user/token missing."}
	}
	c := jenkins.New(jenkins.Config{BaseURL: cfg.BaseURL, User: cfg.User, Token: cfg.Token}, httpc)
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.Ping(pctx); err != nil {
		return doctorCheck{ID: "devops.jenkins", Severity: "error", Message: sanitizeDoctorMessage(fmt.Sprintf("GET /api/json: %v", err))}
	}
	return doctorCheck{ID: "devops.jenkins", Severity: "ok", Message: "Jenkins: /api/json reachable with provided user+token."}
}

func pingSonar(ctx context.Context, cfg config.SonarConfig, httpc *http.Client, timeout time.Duration) doctorCheck {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return doctorCheck{ID: "devops.sonar", Severity: "error", Message: "Sonar enabled but base_url is empty."}
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return doctorCheck{ID: "devops.sonar", Severity: "error", Message: "Sonar enabled but token is empty."}
	}
	c := sonar.New(sonar.Config{BaseURL: cfg.BaseURL, Token: cfg.Token, ProjectKeyPrefix: cfg.ProjectKeyPrefix}, httpc)
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.Ping(pctx); err != nil {
		return doctorCheck{ID: "devops.sonar", Severity: "error", Message: sanitizeDoctorMessage(fmt.Sprintf("GET /api/authentication/validate: %v", err))}
	}
	return doctorCheck{ID: "devops.sonar", Severity: "ok", Message: "Sonar: authentication/validate reachable with provided token."}
}
