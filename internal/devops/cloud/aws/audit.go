package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
)

// AuditEvents reads recent management events via CloudTrail LookupEvents (read-only).
func AuditEvents(ctx context.Context, cfg devopsconfig.CloudAWSConfig, p api.AuditParams) ([]api.AuditEvent, error) {
	if !cfg.Audit {
		return nil, nil
	}
	awsCfg, err := LoadConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if awsCfg.Region == "" {
		awsCfg.Region = cfg.DefaultRegion
	}
	client := cloudtrail.NewFromConfig(awsCfg)

	max := cfg.MaxAuditEvents
	if max <= 0 {
		max = 50
	}

	var out []api.AuditEvent
	var token *string
	for len(out) < max {
		pageSize := int32(max - len(out))
		if pageSize > 50 {
			pageSize = 50
		}
		resp, err := client.LookupEvents(ctx, &cloudtrail.LookupEventsInput{
			StartTime:  aws.Time(p.Start),
			EndTime:    aws.Time(p.End),
			NextToken:  token,
			MaxResults: aws.Int32(pageSize),
		})
		if err != nil {
			return nil, fmt.Errorf("cloudtrail lookup: %w", err)
		}
		for _, ev := range resp.Events {
			if p.Service != "" && ev.EventSource != nil && !strings.Contains(strings.ToLower(*ev.EventSource), strings.ToLower(p.Service)) {
				continue
			}
			ae := api.AuditEvent{
				Provider:   "aws",
				Time:       time.Now().UTC(),
				Action:     aws.ToString(ev.EventName),
				RawSummary: aws.ToString(ev.CloudTrailEvent),
			}
			if ev.EventTime != nil {
				ae.Time = *ev.EventTime
			}
			if ev.Username != nil {
				ae.Actor = *ev.Username
			}
			if ev.Resources != nil && len(ev.Resources) > 0 {
				r0 := ev.Resources[0]
				ae.Resource = aws.ToString(r0.ResourceName)
				ae.Extra = map[string]any{
					"event_id":      aws.ToString(ev.EventId),
					"event_source":  aws.ToString(ev.EventSource),
					"resource_type": aws.ToString(r0.ResourceType),
				}
			} else {
				ae.Extra = map[string]any{
					"event_id":     aws.ToString(ev.EventId),
					"event_source": aws.ToString(ev.EventSource),
				}
			}
			out = append(out, ae)
			if len(out) >= max {
				break
			}
		}
		if resp.NextToken == nil || aws.ToString(resp.NextToken) == "" {
			break
		}
		token = resp.NextToken
	}
	return out, nil
}
