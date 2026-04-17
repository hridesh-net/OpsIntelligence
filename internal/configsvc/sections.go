package configsvc

import (
	"context"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

func (s *Service) SetGateway(ctx context.Context, v config.GatewayConfig) (string, error) {
	return s.Update(ctx, func(cfg *config.Config) error {
		cfg.Gateway = v
		return nil
	})
}

func (s *Service) SetAuth(ctx context.Context, v config.AuthConfig) (string, error) {
	return s.Update(ctx, func(cfg *config.Config) error {
		cfg.Auth = v
		return nil
	})
}

func (s *Service) SetDatastore(ctx context.Context, v config.DatastoreConfig) (string, error) {
	return s.Update(ctx, func(cfg *config.Config) error {
		cfg.Datastore = v
		return nil
	})
}

func (s *Service) SetProviders(ctx context.Context, v config.ProvidersConfig) (string, error) {
	return s.Update(ctx, func(cfg *config.Config) error {
		cfg.Providers = v
		return nil
	})
}

func (s *Service) SetChannels(ctx context.Context, v config.ChannelsConfig) (string, error) {
	return s.Update(ctx, func(cfg *config.Config) error {
		cfg.Channels = v
		return nil
	})
}

func (s *Service) SetWebhooks(ctx context.Context, v config.WebhookConfig) (string, error) {
	return s.Update(ctx, func(cfg *config.Config) error {
		cfg.Webhooks = v
		return nil
	})
}

func (s *Service) SetMCP(ctx context.Context, v config.MCPConfig) (string, error) {
	return s.Update(ctx, func(cfg *config.Config) error {
		cfg.MCP = v
		return nil
	})
}

func (s *Service) SetAgent(ctx context.Context, v config.AgentConfig) (string, error) {
	return s.Update(ctx, func(cfg *config.Config) error {
		cfg.Agent = v
		return nil
	})
}

func (s *Service) SetDevOps(ctx context.Context, v config.DevOpsConfig) (string, error) {
	return s.Update(ctx, func(cfg *config.Config) error {
		cfg.DevOps = v
		return nil
	})
}
