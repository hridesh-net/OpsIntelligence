package pipeline

import (
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/opsintelligence/opsintelligence/internal/devops/sandbox"
)

// TestCommand is a single shell command extracted from a CI config.
type TestCommand struct {
	Shell   string // "bash", "sh", "powershell", or "" (inherit)
	Command string // the command string to execute
	Stage   string // job/stage name this command came from
}

// CIParser extracts runnable test commands from CI configuration content.
// It is purely a structural analysis — it does not execute anything.
type CIParser struct{}

// Parse dispatches to the appropriate parser based on ci.Kind.
// Returns an empty slice when the kind is unknown or parsing fails gracefully.
func (p *CIParser) Parse(ci *sandbox.CIConfig) []TestCommand {
	if ci == nil || ci.Content == "" {
		return nil
	}
	switch ci.Kind {
	case sandbox.CIKindGitHubActions:
		return p.ParseGitHubActions([]byte(ci.Content))
	case sandbox.CIKindGitLabCI:
		return p.ParseGitLabCI([]byte(ci.Content))
	case sandbox.CIKindCircleCI:
		return p.ParseCircleCI([]byte(ci.Content))
	}
	return nil
}

// ── GitHub Actions ────────────────────────────────────────────────────────────

// ghaWorkflow is a minimal representation of a GitHub Actions workflow YAML.
type ghaWorkflow struct {
	Jobs map[string]struct {
		Steps []struct {
			Name  string `yaml:"name"`
			Run   string `yaml:"run"`
			Shell string `yaml:"shell"`
			Uses  string `yaml:"uses"`
			With  map[string]any `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// ParseGitHubActions parses GitHub Actions workflow YAML and returns the
// `run:` steps that look like test/build commands.
func (p *CIParser) ParseGitHubActions(content []byte) []TestCommand {
	var wf ghaWorkflow
	if err := yaml.Unmarshal(content, &wf); err != nil {
		return nil
	}
	var out []TestCommand
	for jobName, job := range wf.Jobs {
		for _, step := range job.Steps {
			cmd := strings.TrimSpace(step.Run)
			if cmd == "" {
				continue
			}
			if !isTestOrBuildCmd(cmd) {
				continue
			}
			shell := step.Shell
			if shell == "" {
				shell = "bash"
			}
			name := step.Name
			if name == "" {
				name = jobName
			}
			out = append(out, TestCommand{
				Shell:   shell,
				Command: cmd,
				Stage:   name,
			})
		}
	}
	return out
}

// ── GitLab CI ─────────────────────────────────────────────────────────────────

// gitlabJob is a minimal representation of a GitLab CI job.
type gitlabJob struct {
	Script     []string `yaml:"script"`
	BeforeScript []string `yaml:"before_script"`
	Stage      string   `yaml:"stage"`
}

// ParseGitLabCI parses .gitlab-ci.yml and extracts script lines from jobs
// that have a test-like stage name or script content.
func (p *CIParser) ParseGitLabCI(content []byte) []TestCommand {
	// GitLab CI YAML has arbitrary top-level keys (one per job).
	// Unmarshal into a raw map first.
	var raw map[string]any
	if err := yaml.Unmarshal(content, &raw); err != nil {
		return nil
	}

	var out []TestCommand
	for jobName, v := range raw {
		// Skip built-in top-level keys.
		switch jobName {
		case "stages", "variables", "cache", "image", "services",
			"before_script", "after_script", "include", "workflow", "default":
			continue
		}
		b, err := yaml.Marshal(v)
		if err != nil {
			continue
		}
		var job gitlabJob
		if err := yaml.Unmarshal(b, &job); err != nil {
			continue
		}
		stage := job.Stage
		if stage == "" {
			stage = jobName
		}
		for _, cmd := range append(job.BeforeScript, job.Script...) {
			cmd = strings.TrimSpace(cmd)
			if cmd == "" || !isTestOrBuildCmd(cmd) {
				continue
			}
			out = append(out, TestCommand{
				Shell:   "bash",
				Command: cmd,
				Stage:   stage,
			})
		}
	}
	return out
}

// ── CircleCI ──────────────────────────────────────────────────────────────────

// circleCIConfig is a minimal representation of a CircleCI config.yml.
type circleCIConfig struct {
	Jobs map[string]struct {
		Steps []any `yaml:"steps"` // each step can be a string or a map
	} `yaml:"jobs"`
}

// ParseCircleCI parses .circleci/config.yml and extracts run: commands.
func (p *CIParser) ParseCircleCI(content []byte) []TestCommand {
	var cfg circleCIConfig
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil
	}
	var out []TestCommand
	for jobName, job := range cfg.Jobs {
		for _, rawStep := range job.Steps {
			switch s := rawStep.(type) {
			case map[string]any:
				// run: "command" or run: {command: "...", name: "..."}
				runVal, ok := s["run"]
				if !ok {
					continue
				}
				var cmd, name string
				switch rv := runVal.(type) {
				case string:
					cmd = rv
					name = jobName
				case map[string]any:
					if c, ok := rv["command"].(string); ok {
						cmd = c
					}
					if n, ok := rv["name"].(string); ok {
						name = n
					} else {
						name = jobName
					}
				}
				cmd = strings.TrimSpace(cmd)
				if cmd != "" && isTestOrBuildCmd(cmd) {
					out = append(out, TestCommand{
						Shell:   "bash",
						Command: cmd,
						Stage:   name,
					})
				}
			}
		}
	}
	return out
}

// ── Heuristic filter ─────────────────────────────────────────────────────────

// testKeywords are patterns that suggest a command runs tests or a build step
// we care about (build, lint, test, check).
var testKeywords = []*regexp.Regexp{
	regexp.MustCompile(`\bgo\s+test\b`),
	regexp.MustCompile(`\bgo\s+build\b`),
	regexp.MustCompile(`\bgo\s+vet\b`),
	regexp.MustCompile(`\bnpm\s+test\b`),
	regexp.MustCompile(`\bnpm\s+run\s+test\b`),
	regexp.MustCompile(`\byarn\s+test\b`),
	regexp.MustCompile(`\byarn\s+run\s+test\b`),
	regexp.MustCompile(`\bpython\s+-m\s+pytest\b`),
	regexp.MustCompile(`\bpytest\b`),
	regexp.MustCompile(`\bmvn\s+test\b`),
	regexp.MustCompile(`\bmaven\b.*test`),
	regexp.MustCompile(`\bgradle\b.*test`),
	regexp.MustCompile(`\bcargo\s+test\b`),
	regexp.MustCompile(`\bcargo\s+build\b`),
	regexp.MustCompile(`\bmake\s+test\b`),
	regexp.MustCompile(`\bmake\s+build\b`),
	regexp.MustCompile(`\bmake\s+check\b`),
	regexp.MustCompile(`\blint\b`),
	regexp.MustCompile(`\bgolangci-lint\b`),
	regexp.MustCompile(`\beslint\b`),
	regexp.MustCompile(`\bflake8\b`),
	regexp.MustCompile(`\bmypy\b`),
	regexp.MustCompile(`\bph?punit\b`),
	regexp.MustCompile(`\brspec\b`),
	regexp.MustCompile(`\bbundle\s+exec\b`),
	regexp.MustCompile(`\bdotnet\s+test\b`),
	regexp.MustCompile(`\bdotnet\s+build\b`),
}

// isTestOrBuildCmd returns true if cmd matches any test/build keyword pattern.
func isTestOrBuildCmd(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, re := range testKeywords {
		if re.MatchString(lower) {
			return true
		}
	}
	return false
}
