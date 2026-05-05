// Package aws implements read-only AWS inventory, cost, and audit using AWS SDK v2.
package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
)

// LoadConfig builds an aws.Config from devops cloud settings (static keys, default chain, optional assume-role).
func LoadConfig(ctx context.Context, c devopsconfig.CloudAWSConfig) (aws.Config, error) {
	var opts []func(*config.LoadOptions) error
	if c.DefaultRegion != "" {
		opts = append(opts, config.WithRegion(c.DefaultRegion))
	}
	if c.AccessKeyID != "" && c.SecretAccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			c.AccessKeyID, c.SecretAccessKey, c.SessionToken,
		)))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, err
	}
	if c.RoleARN == "" {
		return cfg, nil
	}
	stsClient := sts.NewFromConfig(cfg)
	creds := stscreds.NewAssumeRoleProvider(stsClient, c.RoleARN, func(o *stscreds.AssumeRoleOptions) {
		if c.ExternalID != "" {
			o.ExternalID = aws.String(c.ExternalID)
		}
	})
	cfg.Credentials = aws.NewCredentialsCache(creds)
	return cfg, nil
}

// LoadConfigForDiagnose exposes credential loading for devops.diagnose (STS ping).
func LoadConfigForDiagnose(ctx context.Context, c devopsconfig.CloudAWSConfig) (aws.Config, error) {
	return LoadConfig(ctx, c)
}
