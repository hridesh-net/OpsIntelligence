package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

// Result is the outcome of a sandbox pipeline run.
type Result struct {
	CIKind      CIKind
	Succeeded   bool
	Skipped     bool
	SkipReason  string
	Output      string   // combined stdout+stderr, capped at 8KB
	ElapsedSecs int
	Errors      []string // extracted error lines
}

// Runner orchestrates CI pipeline sandbox execution.
type Runner struct {
	cfg      config.PipelineConfig
	detector *Detector
}

// NewRunner creates a Runner with the given config and detector.
func NewRunner(cfg config.PipelineConfig, det *Detector) *Runner {
	return &Runner{cfg: cfg, detector: det}
}

// Run detects the CI config on headRef, downloads the branch, executes the
// pipeline in a sandbox, and returns structured results. Always returns a
// non-nil Result; error is reserved for unrecoverable internal failures.
func (r *Runner) Run(ctx context.Context, owner, repo, headRef string) (*Result, error) {
	ci, err := r.detector.Detect(ctx, owner, repo, headRef)
	if err != nil {
		return &Result{Skipped: true, SkipReason: "pipeline detection failed: " + err.Error()}, nil
	}
	if ci.Kind == CIKindUnknown {
		return &Result{Skipped: true, SkipReason: "no CI config detected in repo"}, nil
	}

	if !r.dockerAvailable() {
		if r.cfg.RequireDocker {
			return nil, fmt.Errorf("sandbox: Docker is not available and require_docker is set")
		}
		return r.dryRunReport(ci), nil
	}

	tmpDir, err := os.MkdirTemp("", "opsintel-sandbox-*")
	if err != nil {
		return &Result{Skipped: true, SkipReason: "failed to create temp dir: " + err.Error()}, nil
	}
	defer os.RemoveAll(tmpDir)

	if err := r.downloadArchive(ctx, owner, repo, headRef, tmpDir); err != nil {
		return &Result{Skipped: true, SkipReason: "failed to download repo archive: " + err.Error()}, nil
	}

	timeout := time.Duration(r.cfg.TimeoutSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	var output string
	var succeeded bool

	switch ci.Kind {
	case CIKindGitHubActions:
		actPath := r.resolveBin(r.cfg.ActPath, "act")
		if actPath != "" {
			output, succeeded, err = r.runWithAct(runCtx, tmpDir, actPath, ci.FilePath)
		} else {
			output, succeeded, err = r.runFallback(runCtx, ci, tmpDir)
		}
	case CIKindGitLabCI:
		runnerPath := r.resolveBin(r.cfg.GitLabRunnerPath, "gitlab-runner")
		if runnerPath != "" {
			output, succeeded, err = r.runWithGitLabRunner(runCtx, tmpDir, runnerPath)
		} else {
			output, succeeded, err = r.runFallback(runCtx, ci, tmpDir)
		}
	case CIKindCircleCI:
		cliPath := r.resolveBin(r.cfg.CircleCIPath, "circleci")
		if cliPath != "" {
			output, succeeded, err = r.runWithCircleCI(runCtx, tmpDir, cliPath)
		} else {
			output, succeeded, err = r.runFallback(runCtx, ci, tmpDir)
		}
	default:
		output, succeeded, err = r.runFallback(runCtx, ci, tmpDir)
	}

	if err != nil {
		return &Result{
			CIKind:      ci.Kind,
			Skipped:     true,
			SkipReason:  "sandbox execution error: " + err.Error(),
			ElapsedSecs: int(time.Since(start).Seconds()),
		}, nil
	}

	return &Result{
		CIKind:      ci.Kind,
		Succeeded:   succeeded,
		Output:      capOutput(output),
		ElapsedSecs: int(time.Since(start).Seconds()),
		Errors:      extractErrors(output),
	}, nil
}

// resolveBin returns the path to a binary: first tries cfg path, then $PATH lookup.
// Returns "" if not found.
func (r *Runner) resolveBin(cfgPath, name string) string {
	if cfgPath != "" {
		if _, err := os.Stat(cfgPath); err == nil {
			return cfgPath
		}
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

// dockerAvailable checks whether Docker daemon is reachable.
func (r *Runner) dockerAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	return cmd.Run() == nil
}

// runWithAct invokes nektos/act to run GitHub Actions locally in Docker.
func (r *Runner) runWithAct(ctx context.Context, workDir, actPath, workflowFile string) (string, bool, error) {
	args := []string{
		"push",
		"--workflows", workflowFile,
		"--platform", "ubuntu-latest=" + r.cfg.ActRunnerImage,
		"--artifact-server-path", "/tmp/artifacts",
		"--no-cache-server",
		"-W", workflowFile,
	}
	return r.runCmd(ctx, workDir, actPath, args...)
}

// runWithGitLabRunner invokes gitlab-runner to execute the first job found.
func (r *Runner) runWithGitLabRunner(ctx context.Context, workDir, runnerPath string) (string, bool, error) {
	job, err := r.firstGitLabJob(workDir)
	if err != nil || job == "" {
		job = "test"
	}
	return r.runCmd(ctx, workDir, runnerPath, "exec", "docker", job)
}

// runWithCircleCI invokes the CircleCI CLI to run the first job locally.
func (r *Runner) runWithCircleCI(ctx context.Context, workDir, cliPath string) (string, bool, error) {
	return r.runCmd(ctx, workDir, cliPath, "local", "execute")
}

// runFallback parses the CI YAML and runs each extracted shell command in a
// Docker Alpine container with no network access and resource limits.
func (r *Runner) runFallback(ctx context.Context, ci *CIConfig, workDir string) (string, bool, error) {
	commands := extractCommands(ci)
	if len(commands) == 0 {
		return "[no runnable commands found in CI config]", true, nil
	}

	var sb strings.Builder
	allSucceeded := true

	for i, cmd := range commands {
		if ctx.Err() != nil {
			break
		}
		sb.WriteString(fmt.Sprintf("$ %s\n", cmd))
		out, ok, err := r.runCmd(ctx, workDir, "docker",
			"run", "--rm",
			"--network", "none",
			"--memory", "512m",
			"--cpus", "1",
			"-v", workDir+":/workspace",
			"-w", "/workspace",
			r.cfg.FallbackImage,
			"sh", "-c", cmd,
		)
		sb.WriteString(out)
		if err != nil || !ok {
			allSucceeded = false
			sb.WriteString(fmt.Sprintf("[step %d failed]\n", i+1))
			break
		}
	}

	return sb.String(), allSucceeded, nil
}

// runCmd executes a command in workDir and returns combined output.
func (r *Runner) runCmd(ctx context.Context, workDir, bin string, args ...string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	output := out.String()
	if err != nil {
		return output, false, nil
	}
	return output, true, nil
}

// downloadArchive downloads the GitHub tarball for owner/repo@ref and extracts
// it into destDir using curl and tar (consistent with existing CLI-based Docker usage).
func (r *Runner) downloadArchive(ctx context.Context, owner, repo, ref, destDir string) error {
	tarURL := fmt.Sprintf("%s/repos/%s/%s/tarball/%s",
		r.detector.baseURL, owner, repo, ref)

	// curl follows the redirect (-L) and pipes into tar for extraction
	curlArgs := []string{"-L", "--silent", "--fail", "-H",
		"Authorization: Bearer " + r.detector.token, tarURL}
	curl := exec.CommandContext(ctx, "curl", curlArgs...)
	tar := exec.CommandContext(ctx, "tar", "xz", "--strip-components=1", "-C", destDir)

	var errBuf bytes.Buffer
	tar.Stdin, _ = curl.StdoutPipe()
	tar.Stdout = &errBuf
	tar.Stderr = &errBuf
	curl.Stderr = &errBuf

	if err := tar.Start(); err != nil {
		return fmt.Errorf("tar start: %w", err)
	}
	if err := curl.Run(); err != nil {
		tar.Wait() //nolint:errcheck
		return fmt.Errorf("curl download: %w: %s", err, errBuf.String())
	}
	if err := tar.Wait(); err != nil {
		return fmt.Errorf("tar extract: %w: %s", err, errBuf.String())
	}
	return nil
}

// dryRunReport produces a skipped result describing what would run.
func (r *Runner) dryRunReport(ci *CIConfig) *Result {
	cmds := extractCommands(ci)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Docker unavailable — dry run only.\nDetected: %s (%s)\n", ciKindLabel(ci.Kind), ci.FilePath))
	if len(cmds) > 0 {
		sb.WriteString(fmt.Sprintf("Would execute %d steps:\n", len(cmds)))
		for i, c := range cmds {
			sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, c))
		}
	}
	return &Result{
		CIKind:     ci.Kind,
		Skipped:    true,
		SkipReason: sb.String(),
	}
}

// firstGitLabJob reads .gitlab-ci.yml in workDir and returns the first non-hidden job name.
func (r *Runner) firstGitLabJob(workDir string) (string, error) {
	data, err := os.ReadFile(workDir + "/.gitlab-ci.yml")
	if err != nil {
		return "", err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return "", err
	}
	reserved := map[string]bool{"stages": true, "variables": true, "cache": true,
		"before_script": true, "after_script": true, "include": true, "default": true}
	for k := range raw {
		if !reserved[k] && !strings.HasPrefix(k, ".") {
			return k, nil
		}
	}
	return "", nil
}

// capOutput trims output to 8KB.
func capOutput(s string) string {
	if len(s) > maxOutputBytes {
		return s[:maxOutputBytes] + "\n[output truncated]"
	}
	return s
}

// extractErrors scans output for common CI failure patterns.
func extractErrors(output string) []string {
	var errs []string
	for _, line := range strings.Split(output, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error:") ||
			strings.Contains(lower, "fatal:") ||
			strings.Contains(lower, "failed:") ||
			strings.Contains(lower, "exit code") ||
			strings.Contains(lower, "panic:") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				errs = append(errs, trimmed)
			}
		}
	}
	// Cap at 10 error lines to keep output tidy
	if len(errs) > 10 {
		errs = errs[:10]
	}
	return errs
}

// ─────────────────────────────────────────────────────────────────────────────
// YAML command extraction (fallback runner)
// ─────────────────────────────────────────────────────────────────────────────

// extractCommands parses a CI YAML config and returns the shell commands to run.
func extractCommands(ci *CIConfig) []string {
	switch ci.Kind {
	case CIKindGitHubActions:
		return extractGitHubActionsCommands(ci.Content)
	case CIKindGitLabCI:
		return extractGitLabCommands(ci.Content)
	case CIKindCircleCI:
		return extractCircleCICommands(ci.Content)
	}
	return nil
}

func extractGitHubActionsCommands(content string) []string {
	var doc struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil
	}
	var cmds []string
	for _, job := range doc.Jobs {
		for _, step := range job.Steps {
			if step.Run != "" {
				cmds = append(cmds, step.Run)
			}
		}
		break // only first job for fallback
	}
	return cmds
}

func extractGitLabCommands(content string) []string {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return nil
	}
	reserved := map[string]bool{"stages": true, "variables": true, "cache": true,
		"before_script": true, "after_script": true, "include": true, "default": true}
	for k, v := range raw {
		if reserved[k] || strings.HasPrefix(k, ".") {
			continue
		}
		job, ok := v.(map[string]any)
		if !ok {
			continue
		}
		scripts, ok := job["script"].([]any)
		if !ok {
			continue
		}
		var cmds []string
		for _, s := range scripts {
			if str, ok := s.(string); ok {
				cmds = append(cmds, str)
			}
		}
		return cmds // first job only
	}
	return nil
}

func extractCircleCICommands(content string) []string {
	var doc struct {
		Jobs map[string]struct {
			Steps []any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil
	}
	for _, job := range doc.Jobs {
		var cmds []string
		for _, step := range job.Steps {
			switch s := step.(type) {
			case map[string]any:
				if run, ok := s["run"]; ok {
					switch rv := run.(type) {
					case string:
						cmds = append(cmds, rv)
					case map[string]any:
						if cmd, ok := rv["command"].(string); ok {
							cmds = append(cmds, cmd)
						}
					}
				}
			}
		}
		if len(cmds) > 0 {
			return cmds // first job only
		}
	}
	return nil
}
