package aws

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	rgttypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/cloud/api"
)

// Inventory lists tagged resources via Resource Groups Tagging API (read-only).
func Inventory(ctx context.Context, cfg devopsconfig.CloudAWSConfig, p api.InventoryParams) ([]api.Resource, error) {
	if !cfg.Inventory {
		return nil, nil
	}
	awsCfg, err := LoadConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	client := resourcegroupstaggingapi.NewFromConfig(awsCfg)

	tagFilters := api.MergeTagFilters(cfg.TagFilters, p.TagFilters)
	var apiFilters []rgttypes.TagFilter
	for _, tf := range tagFilters {
		if tf.Key == "" || len(tf.Values) == 0 {
			continue
		}
		apiFilters = append(apiFilters, rgttypes.TagFilter{
			Key:    aws.String(tf.Key),
			Values: tf.Values,
		})
	}

	max := cfg.MaxResources
	if p.MaxResources > 0 && p.MaxResources < max {
		max = p.MaxResources
	}

	regions := cfg.Regions
	if len(p.Regions) > 0 {
		regions = p.Regions
	}
	regionSet := map[string]struct{}{}
	for _, r := range regions {
		regionSet[strings.TrimSpace(r)] = struct{}{}
	}

	paginator := resourcegroupstaggingapi.NewGetResourcesPaginator(client, &resourcegroupstaggingapi.GetResourcesInput{
		TagFilters: apiFilters,
	})
	var out []api.Resource
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, mapping := range page.ResourceTagMappingList {
			if mapping.ResourceARN == nil {
				continue
			}
			arn := aws.ToString(mapping.ResourceARN)
			region := regionFromARN(arn)
			if len(regionSet) > 0 {
				if _, ok := regionSet[region]; !ok {
					continue
				}
			}
			tags := map[string]string{}
			for _, t := range mapping.Tags {
				if t.Key != nil && t.Value != nil {
					tags[*t.Key] = *t.Value
				}
			}
			out = append(out, api.Resource{
				Provider: "aws",
				ID:       arn,
				Region:   region,
				Type:     resourceTypeFromARN(arn),
				Tags:     tags,
			})
			if len(out) >= max {
				return out, nil
			}
		}
	}
	return out, nil
}

func regionFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) >= 4 {
		return parts[3]
	}
	return ""
}

func resourceTypeFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}
