package gcp

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/logging/logadmin"
	"google.golang.org/api/iterator"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
)

// AuditEvents reads Admin Activity audit log entries (read-only Logging API).
func AuditEvents(ctx context.Context, cfg devopsconfig.CloudGCPConfig, p api.AuditParams) ([]api.AuditEvent, error) {
	if !cfg.Audit {
		return nil, nil
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("gcp: project_id is required")
	}
	client, err := logadmin.NewClient(ctx, cfg.ProjectID, clientOptions(cfg)...)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	logName := fmt.Sprintf(`logName="projects/%s/logs/cloudaudit.googleapis.com%%2Factivity"`, cfg.ProjectID)
	ts := fmt.Sprintf(`timestamp >= "%s" AND timestamp <= "%s"`,
		p.Start.UTC().Format("2006-01-02T15:04:05Z"),
		p.End.UTC().Format("2006-01-02T15:04:05Z"),
	)
	filter := logName + " AND " + ts
	if p.Service != "" {
		filter += fmt.Sprintf(` AND protoPayload.methodName:"%s"`, strings.TrimSpace(p.Service))
	}

	max := cfg.MaxAuditEvents
	it := client.Entries(ctx, logadmin.Filter(filter), logadmin.PageSize(int32(minInt(50, max))))
	var out []api.AuditEvent
	for {
		entry, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		ae := api.AuditEvent{
			Provider:   "gcp",
			Time:       entry.Timestamp,
			RawSummary: fmt.Sprint(entry.Payload),
		}
		if entry.Resource != nil {
			ae.Resource = entry.Resource.Type
		}
		out = append(out, ae)
		if len(out) >= max {
			break
		}
	}
	return out, nil
}
