// Package gcp implements read-only GCP Cloud Asset, billing info, and audit log reads.
package gcp

import (
	"google.golang.org/api/option"

	devopsconfig "github.com/opsintelligence/opsintelligence/internal/config"
)

func clientOptions(cfg devopsconfig.CloudGCPConfig) []option.ClientOption {
	var opts []option.ClientOption
	if p := cfg.CredentialsPath; p != "" {
		opts = append(opts, option.WithCredentialsFile(p))
	}
	return opts
}

func assetParent(cfg devopsconfig.CloudGCPConfig) string {
	return "projects/" + cfg.ProjectID
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
