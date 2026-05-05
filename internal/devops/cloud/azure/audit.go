package azure

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
)

// AuditEvents reads subscription Activity Log (management events, read-only).
func AuditEvents(ctx context.Context, cfg devopsconfig.CloudAzureConfig, p api.AuditParams) ([]api.AuditEvent, error) {
	if !cfg.Audit {
		return nil, nil
	}
	if cfg.SubscriptionID == "" {
		return nil, fmt.Errorf("azure: subscription_id is required")
	}
	cred, err := clientSecretCredential(cfg)
	if err != nil {
		return nil, err
	}
	client, err := armmonitor.NewActivityLogsClient(cfg.SubscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}

	start := p.Start.UTC().Format(time.RFC3339)
	end := p.End.UTC().Format(time.RFC3339)
	filter := fmt.Sprintf("eventTimestamp ge '%s' and eventTimestamp le '%s'", start, end)
	if p.Service != "" {
		filter += fmt.Sprintf(" and resourceProvider eq '%s'", api.EscapeODataQuotes(p.Service))
	}

	max := cfg.MaxAuditEvents
	pager := client.NewListPager(filter, nil)
	var out []api.AuditEvent
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, ev := range page.Value {
			if ev == nil {
				continue
			}
			ae := api.AuditEvent{Provider: "azure"}
			if ev.EventTimestamp != nil {
				ae.Time = *ev.EventTimestamp
			}
			if ev.Caller != nil {
				ae.Actor = *ev.Caller
			}
			if ev.OperationName != nil {
				if ev.OperationName.LocalizedValue != nil {
					ae.Action = *ev.OperationName.LocalizedValue
				} else if ev.OperationName.Value != nil {
					ae.Action = *ev.OperationName.Value
				}
			}
			if ev.ResourceID != nil {
				ae.Resource = *ev.ResourceID
			}
			if ev.Status != nil {
				if ev.Status.LocalizedValue != nil {
					ae.Status = *ev.Status.LocalizedValue
				} else if ev.Status.Value != nil {
					ae.Status = *ev.Status.Value
				}
			}
			if ev.SubStatus != nil {
				if ev.SubStatus.LocalizedValue != nil {
					ae.RawSummary = *ev.SubStatus.LocalizedValue
				} else if ev.SubStatus.Value != nil {
					ae.RawSummary = *ev.SubStatus.Value
				}
			}
			out = append(out, ae)
			if len(out) >= max {
				return out, nil
			}
		}
	}
	return out, nil
}
