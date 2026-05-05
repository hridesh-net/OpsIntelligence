package gcp

import (
	"context"
	"fmt"
	"strings"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	"google.golang.org/api/iterator"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
)

// Inventory uses Cloud Asset Inventory SearchAllResources (read-only).
func Inventory(ctx context.Context, cfg devopsconfig.CloudGCPConfig, p api.InventoryParams) ([]api.Resource, error) {
	if !cfg.Inventory {
		return nil, nil
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("gcp: project_id is required")
	}
	client, err := asset.NewClient(ctx, clientOptions(cfg)...)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	max := cfg.MaxResources
	if p.MaxResources > 0 && p.MaxResources < max {
		max = p.MaxResources
	}

	tagFilters := api.MergeTagFilters(cfg.TagFilters, p.TagFilters)
	var queryParts []string
	for _, tf := range tagFilters {
		if tf.Key == "" || len(tf.Values) == 0 {
			continue
		}
		queryParts = append(queryParts, fmt.Sprintf("labels.%s:%s", tf.Key, tf.Values[0]))
	}
	query := strings.Join(queryParts, " AND ")

	req := &assetpb.SearchAllResourcesRequest{
		Scope:    assetParent(cfg),
		PageSize: int32(minInt(500, max)),
	}
	if query != "" {
		req.Query = query
	}

	regions := cfg.Regions
	if len(p.Regions) > 0 {
		regions = p.Regions
	}
	regionSet := map[string]struct{}{}
	for _, r := range regions {
		regionSet[strings.TrimSpace(r)] = struct{}{}
	}

	it := client.SearchAllResources(ctx, req)
	var out []api.Resource
	for {
		res, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		region := strings.TrimPrefix(strings.TrimPrefix(res.Location, "regions/"), "zones/")
		if idx := strings.LastIndex(region, "/"); idx >= 0 {
			region = region[idx+1:]
		}
		if len(regionSet) > 0 {
			if _, ok := regionSet[region]; !ok {
				continue
			}
		}
		tags := map[string]string{}
		for k, v := range res.Labels {
			tags[k] = v
		}
		out = append(out, api.Resource{
			Provider: "gcp",
			ID:       res.Name,
			Region:   region,
			Type:     res.AssetType,
			Name:     lastSeg(res.Name),
			Tags:     tags,
		})
		if len(out) >= max {
			break
		}
	}
	return out, nil
}

func lastSeg(name string) string {
	i := strings.LastIndex(name, "/")
	if i < 0 {
		return name
	}
	return name[i+1:]
}
