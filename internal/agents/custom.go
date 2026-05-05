package agents

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// CustomAgentDef is the on-disk YAML representation of a user-defined agent.
// Stored at <customAgentsDir>/<name>/agent.yaml.
//
// Example file:
//
//	name: infra
//	description: Specialist for Terraform, Kubernetes, and cloud infrastructure.
//	keywords:
//	  - terraform
//	  - kubernetes
//	  - aws
//	system_prompt: |
//	  ## Infrastructure Specialist Mode
//	  Focus on IaC correctness, resource hygiene, and cloud cost.
//	allowed_tools:
//	  - bash
//	  - read_file
//	  - grep
//	  - memory_search
//	  - web_search
//	blocked_tools:
//	  - subagent_run
//	  - subagent_run_parallel
//	  - subagent_run_async
type CustomAgentDef struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	Keywords     []string `yaml:"keywords"`
	SystemPrompt string   `yaml:"system_prompt,omitempty"`
	AllowedTools []string `yaml:"allowed_tools,omitempty"`
	BlockedTools []string `yaml:"blocked_tools,omitempty"`
}

// ToAgentDef converts a CustomAgentDef to a registry-ready AgentDef.
// Context loaders are not attached here; they are wired by the caller
// (typically main.go) using the same AgentsConfigDir as built-in agents.
func (c CustomAgentDef) ToAgentDef() AgentDef {
	return AgentDef{
		Name:              c.Name,
		Description:       c.Description,
		Keywords:          c.Keywords,
		SystemPromptFocus: c.SystemPrompt,
		AllowedTools:      c.AllowedTools,
		BlockedTools:      c.BlockedTools,
	}
}

// LoadCustomAgents scans dir for subdirectories that contain agent.yaml.
// Valid definitions are returned as AgentDefs. Errors on individual files
// are collected separately so one bad file does not block the rest.
func LoadCustomAgents(dir string) ([]AgentDef, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("list custom agents dir: %w", err)}
	}
	var defs []AgentDef
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		defPath := filepath.Join(dir, e.Name(), "agent.yaml")
		data, err := os.ReadFile(defPath)
		if err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("read %s: %w", defPath, err))
			}
			continue
		}
		var cdef CustomAgentDef
		if err := yaml.Unmarshal(data, &cdef); err != nil {
			errs = append(errs, fmt.Errorf("parse %s: %w", defPath, err))
			continue
		}
		if cdef.Name == "" {
			cdef.Name = e.Name()
		}
		defs = append(defs, cdef.ToAgentDef())
	}
	return defs, errs
}

// WriteCustomAgentDef persists def to <dir>/<def.Name>/agent.yaml, creating
// the directory if it does not exist.
func WriteCustomAgentDef(dir string, def CustomAgentDef) error {
	agentDir := filepath.Join(dir, def.Name)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}
	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("marshal agent def: %w", err)
	}
	return os.WriteFile(filepath.Join(agentDir, "agent.yaml"), data, 0o644)
}

// RemoveCustomAgent deletes <dir>/<name>/ entirely.
// Returns an error when the agent directory does not exist.
func RemoveCustomAgent(dir, name string) error {
	agentDir := filepath.Join(dir, name)
	if _, err := os.Stat(agentDir); os.IsNotExist(err) {
		return fmt.Errorf("custom agent %q not found", name)
	}
	return os.RemoveAll(agentDir)
}

// ListCustomAgentNames returns the names of all subdirectories under dir that
// contain an agent.yaml. Used by the agent.list tool.
func ListCustomAgentNames(dir string) []string {
	var names []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == dir {
			return nil
		}
		if _, e := os.Stat(filepath.Join(path, "agent.yaml")); e == nil {
			if rel, relErr := filepath.Rel(dir, path); relErr == nil {
				names = append(names, rel)
			}
			return fs.SkipDir
		}
		return nil
	})
	return names
}
