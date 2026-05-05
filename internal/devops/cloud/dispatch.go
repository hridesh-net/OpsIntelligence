package cloud

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
	awscloud "github.com/opsintelligence/opsintelligence/internal/devops/cloud/aws"
	azurecloud "github.com/opsintelligence/opsintelligence/internal/devops/cloud/azure"
	gcpcloud "github.com/opsintelligence/opsintelligence/internal/devops/cloud/gcp"
)

// Inventory runs a scoped resource list for the requested provider.
func Inventory(ctx context.Context, cfg config.CloudConfig, p api.InventoryParams) ([]api.Resource, error) {
	switch strings.ToLower(strings.TrimSpace(p.Provider)) {
	case "aws":
		if !cfg.AWS.Enabled || !cfg.AWS.Inventory {
			return nil, fmt.Errorf("aws cloud inventory disabled or not configured")
		}
		return awscloud.Inventory(ctx, cfg.AWS, p)
	case "azure":
		if !cfg.Azure.Enabled || !cfg.Azure.Inventory {
			return nil, fmt.Errorf("azure cloud inventory disabled or not configured")
		}
		return azurecloud.Inventory(ctx, cfg.Azure, p)
	case "gcp":
		if !cfg.GCP.Enabled || !cfg.GCP.Inventory {
			return nil, fmt.Errorf("gcp cloud inventory disabled or not configured")
		}
		return gcpcloud.Inventory(ctx, cfg.GCP, p)
	default:
		return nil, fmt.Errorf("provider must be aws, azure, or gcp")
	}
}

// CostSummary returns summarized spend (provider-specific behavior; see tool docs).
func CostSummary(ctx context.Context, cfg config.CloudConfig, p api.CostParams) ([]api.CostPoint, error) {
	switch strings.ToLower(strings.TrimSpace(p.Provider)) {
	case "aws":
		if !cfg.AWS.Enabled || !cfg.AWS.Cost {
			return nil, fmt.Errorf("aws cost disabled or not configured")
		}
		return awscloud.CostSummary(ctx, cfg.AWS, p)
	case "azure":
		if !cfg.Azure.Enabled || !cfg.Azure.Cost {
			return nil, fmt.Errorf("azure cost disabled or not configured")
		}
		return azurecloud.CostSummary(ctx, cfg.Azure, p)
	case "gcp":
		if !cfg.GCP.Enabled || !cfg.GCP.Cost {
			return nil, fmt.Errorf("gcp cost disabled or not configured")
		}
		return gcpcloud.CostSummary(ctx, cfg.GCP, p)
	default:
		return nil, fmt.Errorf("provider must be aws, azure, or gcp")
	}
}

// AuditEvents returns recent control-plane audit activity.
func AuditEvents(ctx context.Context, cfg config.CloudConfig, p api.AuditParams) ([]api.AuditEvent, error) {
	switch strings.ToLower(strings.TrimSpace(p.Provider)) {
	case "aws":
		if !cfg.AWS.Enabled || !cfg.AWS.Audit {
			return nil, fmt.Errorf("aws audit disabled or not configured")
		}
		return awscloud.AuditEvents(ctx, cfg.AWS, p)
	case "azure":
		if !cfg.Azure.Enabled || !cfg.Azure.Audit {
			return nil, fmt.Errorf("azure audit disabled or not configured")
		}
		return azurecloud.AuditEvents(ctx, cfg.Azure, p)
	case "gcp":
		if !cfg.GCP.Enabled || !cfg.GCP.Audit {
			return nil, fmt.Errorf("gcp audit disabled or not configured")
		}
		return gcpcloud.AuditEvents(ctx, cfg.GCP, p)
	default:
		return nil, fmt.Errorf("provider must be aws, azure, or gcp")
	}
}

// LoadAWSConfigForDiagnose exposes AWS credential loading for devops.diagnose (STS ping).
func LoadAWSConfigForDiagnose(ctx context.Context, c config.CloudAWSConfig) (aws.Config, error) {
	return awscloud.LoadConfigForDiagnose(ctx, c)
}
