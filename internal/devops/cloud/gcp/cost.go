package gcp

import (
	"context"
	"fmt"

	"google.golang.org/api/cloudbilling/v1"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
)

// CostSummary returns project billing linkage and a note about detailed cost APIs.
func CostSummary(ctx context.Context, cfg devopsconfig.CloudGCPConfig, p api.CostParams) ([]api.CostPoint, error) {
	if !cfg.Cost {
		return nil, nil
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("gcp: project_id is required")
	}
	svc, err := cloudbilling.NewService(ctx, clientOptions(cfg)...)
	if err != nil {
		return nil, err
	}
	name := "projects/" + cfg.ProjectID
	info, err := svc.Projects.GetBillingInfo(name).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gcp billing info: %w", err)
	}
	note := "Linked billing resource only. For time-series cost, grant billing account viewer or use BigQuery billing export and query outside this tool."
	return []api.CostPoint{{
		Provider: "gcp",
		Start:    p.Start.UTC().Format("2006-01-02"),
		End:      p.End.UTC().Format("2006-01-02"),
		Amount:   "",
		Service:  info.BillingAccountName,
		Note:     note,
	}}, nil
}
