package cloud

import "github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"

// Re-export stable DTO names so callers can import only package cloud.

type (
	Resource         = api.Resource
	CostPoint        = api.CostPoint
	AuditEvent       = api.AuditEvent
	InventoryParams  = api.InventoryParams
	CostParams       = api.CostParams
	AuditParams      = api.AuditParams
)
