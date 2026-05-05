package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
)

// CostSummary returns blended cost by day or month via Cost Explorer (read-only).
func CostSummary(ctx context.Context, cfg devopsconfig.CloudAWSConfig, p api.CostParams) ([]api.CostPoint, error) {
	if !cfg.Cost {
		return nil, nil
	}
	awsCfg, err := LoadConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	awsCfg.Region = "us-east-1"

	client := costexplorer.NewFromConfig(awsCfg)

	gran := types.GranularityDaily
	if p.Granularity == "MONTHLY" {
		gran = types.GranularityMonthly
	}

	start := p.Start.UTC().Format("2006-01-02")
	end := p.End.UTC().Format("2006-01-02")
	if end == start {
		end = p.End.UTC().Add(24 * time.Hour).Format("2006-01-02")
	}

	maxPoints := cfg.MaxCostPoints
	if maxPoints <= 0 {
		maxPoints = 62
	}

	out, err := client.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
		TimePeriod:  &types.DateInterval{Start: aws.String(start), End: aws.String(end)},
		Granularity: gran,
		Metrics:     []string{"UnblendedCost"},
		GroupBy: []types.GroupDefinition{{
			Type: types.GroupDefinitionTypeDimension,
			Key:  aws.String("SERVICE"),
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("aws cost explorer: %w", err)
	}

	var points []api.CostPoint
	for _, day := range out.ResultsByTime {
		if day.TimePeriod == nil || day.TimePeriod.Start == nil {
			continue
		}
		for _, g := range day.Groups {
			if len(g.Keys) == 0 {
				continue
			}
			svc := g.Keys[0]
			amt := ""
			cur := "USD"
			if m, ok := g.Metrics["UnblendedCost"]; ok {
				amt = aws.ToString(m.Amount)
				cur = aws.ToString(m.Unit)
			}
			points = append(points, api.CostPoint{
				Provider:    "aws",
				Start:       aws.ToString(day.TimePeriod.Start),
				End:         aws.ToString(day.TimePeriod.End),
				Amount:      amt,
				Currency:    cur,
				Service:     svc,
				Granularity: string(gran),
			})
			if len(points) >= maxPoints {
				return points, nil
			}
		}
	}
	return points, nil
}
