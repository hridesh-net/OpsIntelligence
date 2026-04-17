package configsvc

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

func (s *Service) SetSkillEnabled(ctx context.Context, skillName string, enabled bool) (string, error) {
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return "", fmt.Errorf("configsvc: skill name is required")
	}
	return s.Update(ctx, func(cfg *config.Config) error {
		existing := append([]string(nil), cfg.Agent.EnabledSkills...)
		if enabled {
			if !slices.Contains(existing, skillName) {
				existing = append(existing, skillName)
			}
		} else {
			filtered := existing[:0]
			for _, v := range existing {
				if v != skillName {
					filtered = append(filtered, v)
				}
			}
			existing = filtered
		}
		cfg.Agent.EnabledSkills = existing
		return nil
	})
}

func (s *Service) AddMCPClient(ctx context.Context, client config.MCPClientConfig) (string, error) {
	name := strings.TrimSpace(client.Name)
	if name == "" {
		return "", fmt.Errorf("configsvc: MCP client name is required")
	}
	client.Name = name
	return s.Update(ctx, func(cfg *config.Config) error {
		for _, c := range cfg.MCP.Clients {
			if c.Name == name {
				return fmt.Errorf("MCP server %q already registered", name)
			}
		}
		cfg.MCP.Clients = append(cfg.MCP.Clients, client)
		return nil
	})
}

func (s *Service) RemoveMCPClient(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("configsvc: MCP client name is required")
	}
	return s.Update(ctx, func(cfg *config.Config) error {
		remaining := make([]config.MCPClientConfig, 0, len(cfg.MCP.Clients))
		found := false
		for _, c := range cfg.MCP.Clients {
			if c.Name == name {
				found = true
				continue
			}
			remaining = append(remaining, c)
		}
		if !found {
			return fmt.Errorf("MCP server %q not found", name)
		}
		cfg.MCP.Clients = remaining
		return nil
	})
}
