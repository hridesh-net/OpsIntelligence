// Package api holds shared DTOs and helpers for multi-cloud read-only integrations.
// Provider implementations live in sibling packages (aws, azure, gcp) to avoid import cycles.
package api

import (
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

// Resource is a normalized row for inventory output.
type Resource struct {
	Provider string            `json:"provider"`
	ID       string            `json:"id"`
	Region   string            `json:"region,omitempty"`
	Type     string            `json:"type,omitempty"`
	Name     string            `json:"name,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	Extra    map[string]any    `json:"extra,omitempty"`
}

// CostPoint is one bucket in a cost summary.
type CostPoint struct {
	Provider    string `json:"provider,omitempty"`
	Start       string `json:"start"`
	End         string `json:"end,omitempty"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency,omitempty"`
	Service     string `json:"service,omitempty"`
	Granularity string `json:"granularity,omitempty"`
	Note        string `json:"note,omitempty"`
}

// AuditEvent is a normalized control-plane activity record.
type AuditEvent struct {
	Provider   string         `json:"provider,omitempty"`
	Time       time.Time      `json:"time"`
	Actor      string         `json:"actor,omitempty"`
	Action     string         `json:"action,omitempty"`
	Resource   string         `json:"resource,omitempty"`
	Region     string         `json:"region,omitempty"`
	Status     string         `json:"status,omitempty"`
	RawSummary string         `json:"raw_summary,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

// InventoryParams overrides config scope for a single tool call.
type InventoryParams struct {
	Provider      string
	Regions       []string
	TagFilters    []config.CloudTagFilter
	MaxResources  int
	ResourceGroup string // Azure-only hint
}

// CostParams describes a cost query window.
type CostParams struct {
	Provider    string
	Start       time.Time
	End         time.Time
	Granularity string // DAILY | MONTHLY (AWS); Azure query API is daily-only
}

// AuditParams describes an audit query window.
type AuditParams struct {
	Provider string
	Start    time.Time
	End      time.Time
	Service  string // optional filter substring
}

// MergeTagFilters concatenates config-level and call-level tag filters (AND semantics at the provider).
func MergeTagFilters(a, b []config.CloudTagFilter) []config.CloudTagFilter {
	out := append([]config.CloudTagFilter{}, a...)
	out = append(out, b...)
	return out
}

// EscapeODataQuotes escapes single quotes for OData filters.
func EscapeODataQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
