package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
)

// Inventory lists resources in a subscription (read-only ARM).
func Inventory(ctx context.Context, cfg devopsconfig.CloudAzureConfig, p api.InventoryParams) ([]api.Resource, error) {
	if !cfg.Inventory {
		return nil, nil
	}
	if cfg.SubscriptionID == "" {
		return nil, fmt.Errorf("azure: subscription_id is required")
	}
	cred, err := clientSecretCredential(cfg)
	if err != nil {
		return nil, err
	}
	client, err := armresources.NewClient(cfg.SubscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}

	max := cfg.MaxResources
	if p.MaxResources > 0 && p.MaxResources < max {
		max = p.MaxResources
	}
	top := int32(max)
	if top > 800 {
		top = 800
	}

	tagFilters := api.MergeTagFilters(cfg.TagFilters, p.TagFilters)
	filter := armResourceFilter(cfg, p, tagFilters)
	opts := &armresources.ClientListOptions{
		Top:    to.Ptr(top),
		Filter: filter,
	}

	pager := client.NewListPager(opts)
	var out []api.Resource
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range page.Value {
			if r == nil || r.ID == nil {
				continue
			}
			region := ""
			if r.Location != nil {
				region = strings.ToLower(*r.Location)
			}
			if !regionAllowed(region, cfg.Regions, p.Regions) {
				continue
			}
			res := api.Resource{
				Provider: "azure",
				ID:       *r.ID,
				Region:   region,
				Type:     deref(r.Type),
				Name:     deref(r.Name),
				Tags:     tagMap(r.Tags),
			}
			out = append(out, res)
			if len(out) >= max {
				return out, nil
			}
		}
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func tagMap(m map[string]*string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

func armResourceFilter(cfg devopsconfig.CloudAzureConfig, p api.InventoryParams, tags []devopsconfig.CloudTagFilter) *string {
	var parts []string
	rg := strings.TrimSpace(p.ResourceGroup)
	if rg == "" {
		rg = strings.TrimSpace(cfg.ResourceGroup)
	}
	if rg != "" {
		parts = append(parts, fmt.Sprintf("resourceGroup eq '%s'", api.EscapeODataQuotes(rg)))
	}
	for _, tf := range tags {
		if tf.Key == "" || len(tf.Values) == 0 {
			continue
		}
		v := tf.Values[0]
		parts = append(parts, fmt.Sprintf("tagName eq '%s' and tagValue eq '%s'", api.EscapeODataQuotes(tf.Key), api.EscapeODataQuotes(v)))
	}
	if len(parts) == 0 {
		return nil
	}
	return to.Ptr(strings.Join(parts, " and "))
}

func regionAllowed(region string, cfgRegions, pRegions []string) bool {
	regions := cfgRegions
	if len(pRegions) > 0 {
		regions = pRegions
	}
	if len(regions) == 0 {
		return true
	}
	for _, r := range regions {
		if strings.EqualFold(strings.TrimSpace(r), region) {
			return true
		}
	}
	return false
}
