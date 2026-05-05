// Package cloudtools registers devops.cloud.* agent tools (read-only inventory, cost, audit).
// Provider execution is delegated to internal/devops/cloud dispatch.
package cloudtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/config"
	devopscloud "github.com/opsintelligence/opsintelligence/internal/devops/cloud"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// Tools returns agent tools when at least one cloud provider is enabled in config.
func Tools(cfg config.CloudConfig) []agent.Tool {
	if !cfg.AWS.Enabled && !cfg.Azure.Enabled && !cfg.GCP.Enabled {
		return nil
	}
	return []agent.Tool{
		&inventoryTool{cfg: cfg},
		&costTool{cfg: cfg},
		&auditTool{cfg: cfg},
	}
}

type inventoryTool struct {
	cfg config.CloudConfig
}

func (t *inventoryTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.cloud.inventory",
		Description: "List cloud resources in scope (read-only). Provider: aws | azure | gcp. Uses devops.cloud.* config: regions, tag_filters, max_resources. AWS: Resource Groups Tagging API. Azure: ARM resource list. GCP: Cloud Asset Inventory.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"provider":       map[string]any{"type": "string", "description": "aws | azure | gcp", "enum": []string{"aws", "azure", "gcp"}},
				"regions":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"resource_group": map[string]any{"type": "string", "description": "Azure only: optional resource group name."},
				"tag_filters": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"key":    map[string]any{"type": "string"},
							"values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
					},
				},
				"max_resources": map[string]any{"type": "integer", "description": "Cap results (default from config)."},
			},
			Required: []string{"provider"},
		},
	}
}

func (t *inventoryTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Provider      string                  `json:"provider"`
		Regions       []string                `json:"regions"`
		ResourceGroup string                  `json:"resource_group"`
		TagFilters    []config.CloudTagFilter `json:"tag_filters"`
		MaxResources  int                     `json:"max_resources"`
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	p := api.InventoryParams{
		Provider:      strings.ToLower(strings.TrimSpace(a.Provider)),
		Regions:       a.Regions,
		TagFilters:    a.TagFilters,
		MaxResources:  a.MaxResources,
		ResourceGroup: a.ResourceGroup,
	}
	resources, err := devopscloud.Inventory(ctx, t.cfg, p)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(resources, "", "  ")
	return string(b), nil
}

type costTool struct {
	cfg config.CloudConfig
}

func (t *costTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.cloud.cost_summary",
		Description: "Summarized cloud spend (read-only). AWS: Cost Explorer by service. Azure: Cost Management query (daily granularity). GCP: billing account linkage + note (detailed cost needs billing export). Use ISO start/end dates.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"provider":    map[string]any{"type": "string", "enum": []string{"aws", "azure", "gcp"}},
				"start":       map[string]any{"type": "string", "description": "Start date (YYYY-MM-DD)."},
				"end":         map[string]any{"type": "string", "description": "End date (YYYY-MM-DD), exclusive for AWS."},
				"granularity": map[string]any{"type": "string", "description": "DAILY or MONTHLY (AWS; Azure API is daily-only).", "enum": []string{"DAILY", "MONTHLY"}},
			},
			Required: []string{"provider", "start", "end"},
		},
	}
}

func (t *costTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Provider    string `json:"provider"`
		Start       string `json:"start"`
		End         string `json:"end"`
		Granularity string `json:"granularity"`
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	start, err := time.Parse("2006-01-02", strings.TrimSpace(a.Start))
	if err != nil {
		return "", fmt.Errorf("start date: %w", err)
	}
	end, err := time.Parse("2006-01-02", strings.TrimSpace(a.End))
	if err != nil {
		return "", fmt.Errorf("end date: %w", err)
	}
	if a.Granularity == "" {
		a.Granularity = "DAILY"
	}
	p := api.CostParams{
		Provider:    strings.ToLower(strings.TrimSpace(a.Provider)),
		Start:       start,
		End:         end,
		Granularity: strings.ToUpper(a.Granularity),
	}
	points, err := devopscloud.CostSummary(ctx, t.cfg, p)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(points, "", "  ")
	return string(b), nil
}

type auditTool struct {
	cfg config.CloudConfig
}

func (t *auditTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.cloud.audit_events",
		Description: "Recent control-plane audit events (read-only). AWS: CloudTrail LookupEvents. Azure: Activity Log. GCP: Cloud Audit activity log. Pass RFC3339 or YYYY-MM-DD for start/end.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"provider": map[string]any{"type": "string", "enum": []string{"aws", "azure", "gcp"}},
				"start":    map[string]any{"type": "string"},
				"end":      map[string]any{"type": "string"},
				"service":  map[string]any{"type": "string", "description": "Optional filter: AWS event source substring, Azure resource provider, GCP method substring."},
			},
			Required: []string{"provider", "start", "end"},
		},
	}
}

func (t *auditTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Provider string `json:"provider"`
		Start    string `json:"start"`
		End      string `json:"end"`
		Service  string `json:"service"`
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	start, end, err := parseAuditWindow(a.Start, a.End)
	if err != nil {
		return "", err
	}
	p := api.AuditParams{
		Provider: strings.ToLower(strings.TrimSpace(a.Provider)),
		Start:    start,
		End:      end,
		Service:  strings.TrimSpace(a.Service),
	}
	events, err := devopscloud.AuditEvents(ctx, t.cfg, p)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(events, "", "  ")
	return string(b), nil
}

func parseAuditWindow(startS, endS string) (time.Time, time.Time, error) {
	startS, endS = strings.TrimSpace(startS), strings.TrimSpace(endS)
	var start, end time.Time
	var err error
	if len(startS) == 10 {
		start, err = time.Parse("2006-01-02", startS)
	} else {
		start, err = time.Parse(time.RFC3339, startS)
	}
	if err != nil {
		return start, end, fmt.Errorf("start time: %w", err)
	}
	if len(endS) == 10 {
		end, err = time.Parse("2006-01-02", endS)
		end = end.Add(24*time.Hour - time.Nanosecond)
	} else {
		end, err = time.Parse(time.RFC3339, endS)
	}
	if err != nil {
		return start, end, fmt.Errorf("end time: %w", err)
	}
	if !end.After(start) {
		return start, end, fmt.Errorf("end must be after start")
	}
	return start, end, nil
}
