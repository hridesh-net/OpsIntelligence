// Package gemini implements the Google Gemini (AI Studio) provider for OpsIntelligence.
// Uses the OpenAI-compatible endpoint so no custom protocol code is needed —
// just an API key from https://aistudio.google.com/app/apikey
package gemini

import (
	"context"

	"github.com/opsintelligence/opsintelligence/internal/provider"
	"github.com/opsintelligence/opsintelligence/internal/provider/catalogs"
	"github.com/opsintelligence/opsintelligence/internal/provider/openaicompat"
)

const (
	providerName         = "google"
	openaiCompatEndpoint = "https://generativelanguage.googleapis.com/v1beta/openai/"
)

// Config holds provider-level settings.
type Config struct {
	APIKey       string `yaml:"api_key" json:"api_key"`
	BaseURL      string `yaml:"base_url" json:"base_url"`
	DefaultModel string `yaml:"default_model" json:"default_model"`
}

// Provider wraps openaicompat with Gemini-specific defaults.
type Provider struct {
	inner *openaicompat.Provider
}

// New creates a new Gemini provider from config.
func New(cfg Config) *Provider {
	base := cfg.BaseURL
	if base == "" {
		base = openaiCompatEndpoint
	}
	return &Provider{
		inner: openaicompat.New(openaicompat.Config{
			Name:           providerName,
			BaseURL:        base,
			APIKey:         cfg.APIKey,
			DefaultModel:   cfg.DefaultModel,
			StaticModels:   catalogs.GeminiModels(providerName),
			DiscoverModels: true,
		}),
	}
}

// Name returns "google" (consistent with existing vertex provider naming).
func (p *Provider) Name() string { return providerName }

// Complete delegates to the OpenAI-compatible inner provider.
func (p *Provider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	return p.inner.Complete(ctx, req)
}

// Stream delegates to the OpenAI-compatible inner provider.
func (p *Provider) Stream(ctx context.Context, req *provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	return p.inner.Stream(ctx, req)
}

// ListModels delegates to the inner provider.
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return p.inner.ListModels(ctx)
}

// HealthCheck delegates to the inner provider.
func (p *Provider) HealthCheck(ctx context.Context) error {
	return p.inner.HealthCheck(ctx)
}

// ValidateModel delegates to the inner provider.
func (p *Provider) ValidateModel(ctx context.Context, modelID string) error {
	return p.inner.ValidateModel(ctx, modelID)
}
