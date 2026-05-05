// Package azure implements read-only Azure ARM inventory, Cost Management, and Activity Log reads.
package azure

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
)

func clientSecretCredential(cfg devopsconfig.CloudAzureConfig) (*azidentity.ClientSecretCredential, error) {
	if cfg.TenantID == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("azure: tenant_id, client_id, and client_secret are required")
	}
	return azidentity.NewClientSecretCredential(cfg.TenantID, cfg.ClientID, cfg.ClientSecret, nil)
}
