package azure

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/costmanagement/armcostmanagement"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
)

// CostSummary queries Cost Management for subscription usage (read-only).
// The Query API supports Daily granularity only.
func CostSummary(ctx context.Context, cfg devopsconfig.CloudAzureConfig, p api.CostParams) ([]api.CostPoint, error) {
	if !cfg.Cost {
		return nil, nil
	}
	if cfg.SubscriptionID == "" {
		return nil, fmt.Errorf("azure: subscription_id is required")
	}
	cred, err := clientSecretCredential(cfg)
	if err != nil {
		return nil, err
	}
	factory, err := armcostmanagement.NewClientFactory(cred, nil)
	if err != nil {
		return nil, err
	}

	gran := armcostmanagement.GranularityTypeDaily

	from := p.Start.UTC()
	toT := p.End.UTC()
	if !toT.After(from) {
		toT = from.Add(24 * time.Hour)
	}

	def := armcostmanagement.QueryDefinition{
		Type: to.Ptr(armcostmanagement.ExportTypeActualCost),
		Dataset: &armcostmanagement.QueryDataset{
			Granularity: to.Ptr(gran),
			Aggregation: map[string]*armcostmanagement.QueryAggregation{
				"totalCost": {
					Name:     to.Ptr("PreTaxCost"),
					Function: to.Ptr(armcostmanagement.FunctionTypeSum),
				},
			},
			Grouping: []*armcostmanagement.QueryGrouping{
				{Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension), Name: to.Ptr("ServiceName")},
			},
		},
		Timeframe: to.Ptr(armcostmanagement.TimeframeTypeCustom),
		TimePeriod: &armcostmanagement.QueryTimePeriod{
			From: to.Ptr(from),
			To:   to.Ptr(toT),
		},
	}

	scope := "subscriptions/" + cfg.SubscriptionID
	res, err := factory.NewQueryClient().Usage(ctx, scope, def, nil)
	if err != nil {
		return nil, fmt.Errorf("azure cost management: %w", err)
	}
	if res.Properties == nil || len(res.Properties.Rows) == 0 {
		return []api.CostPoint{{Provider: "azure", Note: "no cost rows (check billing scope and Cost Management permissions)"}}, nil
	}

	max := cfg.MaxCostPoints
	if max <= 0 {
		max = 62
	}
	var out []api.CostPoint
	cols := res.Properties.Columns
	for _, row := range res.Properties.Rows {
		pt := api.CostPoint{Provider: "azure", Granularity: string(gran)}
		for i, c := range cols {
			if c == nil || i >= len(row) {
				continue
			}
			name := ""
			if c.Name != nil {
				name = *c.Name
			}
			switch name {
			case "PreTaxCost", "Cost", "totalCost":
				pt.Amount = fmt.Sprint(row[i])
			case "Currency":
				pt.Currency = fmt.Sprint(row[i])
			case "ServiceName":
				pt.Service = fmt.Sprint(row[i])
			case "UsageDate":
				pt.Start = fmt.Sprint(row[i])
			}
		}
		out = append(out, pt)
		if len(out) >= max {
			break
		}
	}
	return out, nil
}
