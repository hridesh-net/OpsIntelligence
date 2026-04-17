package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/configsvc"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
	"go.uber.org/zap"
)

type configResponse struct {
	Revision string `json:"revision"`
	Config   any    `json:"config"`
}

func (s *AuthService) handleConfigRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p := auth.PrincipalFrom(r.Context())
	if err := rbac.Enforce(r.Context(), p, rbac.PermSettingsRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	snap, err := s.cfgSvc().Read(r.Context())
	if err != nil {
		s.Log.Error("config read", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "config read failed")
		return
	}
	cfg := snap.Config
	if !rbac.Can(p, rbac.PermSecretsRead) {
		cfg = redactedConfig(cfg)
	}
	writeJSON(w, http.StatusOK, configResponse{
		Revision: snap.Revision,
		Config:   cfg,
	})
}

func (s *AuthService) handleConfigSections(w http.ResponseWriter, r *http.Request) {
	section := strings.TrimPrefix(r.URL.Path, "/api/v1/config/")
	section = strings.Trim(section, "/")
	if section == "" {
		writeJSONError(w, http.StatusNotFound, "config section not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleConfigSectionGet(w, r, section)
	case http.MethodPut:
		s.handleConfigSectionPut(w, r, section)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *AuthService) handleConfigSectionGet(w http.ResponseWriter, r *http.Request, section string) {
	p := auth.PrincipalFrom(r.Context())
	if err := rbac.Enforce(r.Context(), p, rbac.PermSettingsRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	snap, err := s.cfgSvc().Read(r.Context())
	if err != nil {
		s.Log.Error("config section read", zap.String("section", section), zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "config read failed")
		return
	}
	val, ok := sectionValue(snap.Config, section)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "unknown config section")
		return
	}
	if !rbac.Can(p, rbac.PermSecretsRead) {
		val = redactedSection(section, val)
	}
	writeJSON(w, http.StatusOK, configResponse{
		Revision: snap.Revision,
		Config:   val,
	})
}

func (s *AuthService) handleConfigSectionPut(w http.ResponseWriter, r *http.Request, section string) {
	p := auth.PrincipalFrom(r.Context())
	if err := rbac.Enforce(r.Context(), p, rbac.PermSettingsWrite); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if sectionNeedsSecretsPermission(section) && !rbac.Can(p, rbac.PermSecretsWrite) {
		writeJSONError(w, http.StatusForbidden, "secrets.write permission required")
		return
	}

	rev := strings.TrimSpace(r.Header.Get("If-Match"))
	newRev, err := s.putConfigSection(r, section, rev)
	if err != nil {
		switch {
		case errors.Is(err, configsvc.ErrRevisionConflict):
			writeJSONError(w, http.StatusConflict, "config revision conflict")
		case errors.Is(err, errUnknownConfigSection):
			writeJSONError(w, http.StatusNotFound, "unknown config section")
		case errors.Is(err, errInvalidConfigPayload):
			writeJSONError(w, http.StatusBadRequest, "invalid config payload")
		default:
			s.Log.Error("config section write", zap.String("section", section), zap.Error(err))
			writeJSONError(w, http.StatusInternalServerError, "config update failed")
		}
		_ = s.appendConfigAudit(r, p, "config.update", section, false, err)
		return
	}

	_ = s.appendConfigAudit(r, p, "config.update", section, true, nil)
	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"section":  section,
		"revision": newRev,
	})
}

var (
	errUnknownConfigSection = errors.New("unknown config section")
	errInvalidConfigPayload = errors.New("invalid config payload")
)

func (s *AuthService) putConfigSection(r *http.Request, section, expectedRevision string) (string, error) {
	switch section {
	case "gateway":
		var v config.GatewayConfig
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			return "", errInvalidConfigPayload
		}
		return s.cfgSvc().UpdateWithRevision(r.Context(), expectedRevision, func(cfg *config.Config) error {
			cfg.Gateway = v
			return nil
		})
	case "auth":
		var v config.AuthConfig
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			return "", errInvalidConfigPayload
		}
		return s.cfgSvc().UpdateWithRevision(r.Context(), expectedRevision, func(cfg *config.Config) error {
			cfg.Auth = v
			return nil
		})
	case "datastore":
		var v config.DatastoreConfig
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			return "", errInvalidConfigPayload
		}
		return s.cfgSvc().UpdateWithRevision(r.Context(), expectedRevision, func(cfg *config.Config) error {
			cfg.Datastore = v
			return nil
		})
	case "providers":
		var v config.ProvidersConfig
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			return "", errInvalidConfigPayload
		}
		return s.cfgSvc().UpdateWithRevision(r.Context(), expectedRevision, func(cfg *config.Config) error {
			cfg.Providers = v
			return nil
		})
	case "channels":
		var v config.ChannelsConfig
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			return "", errInvalidConfigPayload
		}
		return s.cfgSvc().UpdateWithRevision(r.Context(), expectedRevision, func(cfg *config.Config) error {
			cfg.Channels = v
			return nil
		})
	case "webhooks":
		var v config.WebhookConfig
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			return "", errInvalidConfigPayload
		}
		return s.cfgSvc().UpdateWithRevision(r.Context(), expectedRevision, func(cfg *config.Config) error {
			cfg.Webhooks = v
			return nil
		})
	case "mcp":
		var v config.MCPConfig
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			return "", errInvalidConfigPayload
		}
		return s.cfgSvc().UpdateWithRevision(r.Context(), expectedRevision, func(cfg *config.Config) error {
			cfg.MCP = v
			return nil
		})
	case "agent":
		var v config.AgentConfig
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			return "", errInvalidConfigPayload
		}
		return s.cfgSvc().UpdateWithRevision(r.Context(), expectedRevision, func(cfg *config.Config) error {
			cfg.Agent = v
			return nil
		})
	case "devops":
		var v config.DevOpsConfig
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			return "", errInvalidConfigPayload
		}
		return s.cfgSvc().UpdateWithRevision(r.Context(), expectedRevision, func(cfg *config.Config) error {
			cfg.DevOps = v
			return nil
		})
	default:
		return "", errUnknownConfigSection
	}
}

func sectionValue(cfg *config.Config, section string) (any, bool) {
	switch section {
	case "gateway":
		return cfg.Gateway, true
	case "auth":
		return cfg.Auth, true
	case "datastore":
		return cfg.Datastore, true
	case "providers":
		return cfg.Providers, true
	case "channels":
		return cfg.Channels, true
	case "webhooks":
		return cfg.Webhooks, true
	case "mcp":
		return cfg.MCP, true
	case "agent":
		return cfg.Agent, true
	case "devops":
		return cfg.DevOps, true
	default:
		return nil, false
	}
}

func sectionNeedsSecretsPermission(section string) bool {
	switch section {
	case "auth", "providers", "channels", "webhooks", "mcp", "devops", "gateway", "datastore":
		return true
	default:
		return false
	}
}

func (s *AuthService) appendConfigAudit(r *http.Request, p *auth.Principal, action, section string, success bool, err error) error {
	if s == nil || s.Store == nil {
		return nil
	}
	entry := &datastore.AuditEntry{
		ActorType:    actorTypeFromPrincipal(p),
		ActorID:      actorIDFromPrincipal(p),
		Action:       action,
		ResourceType: "config",
		ResourceID:   section,
		RemoteAddr:   r.RemoteAddr,
		UserAgent:    r.UserAgent(),
		Success:      success,
		Metadata: map[string]any{
			"path":   r.URL.Path,
			"method": r.Method,
		},
	}
	if err != nil {
		entry.ErrorMessage = err.Error()
	}
	return s.Store.Audit().Append(r.Context(), entry)
}

func actorTypeFromPrincipal(p *auth.Principal) datastore.ActorType {
	if p == nil {
		return datastore.ActorSystem
	}
	switch p.Type {
	case auth.PrincipalUser:
		return datastore.ActorUser
	case auth.PrincipalAPIKey:
		return datastore.ActorAPIKey
	default:
		return datastore.ActorSystem
	}
}

func actorIDFromPrincipal(p *auth.Principal) string {
	if p == nil {
		return ""
	}
	switch p.Type {
	case auth.PrincipalUser:
		return p.UserID
	case auth.PrincipalAPIKey:
		return p.APIKeyID
	default:
		return p.Username
	}
}

func redactedConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	out := *cfg
	out.Gateway.Token = ""
	out.Auth.LegacySharedToken = ""
	out.DevOps.GitHub.Token = ""
	out.DevOps.GitLab.Token = ""
	out.DevOps.Jenkins.Token = ""
	out.DevOps.Sonar.Token = ""
	if out.Channels.Slack != nil {
		cp := *out.Channels.Slack
		cp.BotToken = ""
		cp.AppToken = ""
		out.Channels.Slack = &cp
	}
	out.Webhooks.Token = ""
	out.Webhooks.Adapters.GitHub.Secret = ""
	out.Datastore.DSN = ""
	out.MCP.Server.AuthToken = ""
	for i := range out.MCP.Clients {
		out.MCP.Clients[i].AuthToken = ""
	}
	redactProviderCreds(&out.Providers)
	return &out
}

func redactedSection(section string, val any) any {
	switch section {
	case "gateway":
		v := val.(config.GatewayConfig)
		v.Token = ""
		return v
	case "auth":
		v := val.(config.AuthConfig)
		v.LegacySharedToken = ""
		return v
	case "datastore":
		v := val.(config.DatastoreConfig)
		v.DSN = ""
		return v
	case "providers":
		v := val.(config.ProvidersConfig)
		redactProviderCreds(&v)
		return v
	case "channels":
		v := val.(config.ChannelsConfig)
		if v.Slack != nil {
			cp := *v.Slack
			cp.BotToken = ""
			cp.AppToken = ""
			v.Slack = &cp
		}
		return v
	case "webhooks":
		v := val.(config.WebhookConfig)
		v.Token = ""
		v.Adapters.GitHub.Secret = ""
		return v
	case "mcp":
		v := val.(config.MCPConfig)
		v.Server.AuthToken = ""
		for i := range v.Clients {
			v.Clients[i].AuthToken = ""
		}
		return v
	case "devops":
		v := val.(config.DevOpsConfig)
		v.GitHub.Token = ""
		v.GitLab.Token = ""
		v.Jenkins.Token = ""
		v.Sonar.Token = ""
		return v
	default:
		return val
	}
}

func redactProviderCreds(p *config.ProvidersConfig) {
	if p == nil {
		return
	}
	if p.OpenAI != nil {
		p.OpenAI.APIKey = ""
	}
	if p.AzureOpenAI != nil {
		p.AzureOpenAI.APIKey = ""
	}
	if p.Anthropic != nil {
		p.Anthropic.APIKey = ""
	}
	if p.Groq != nil {
		p.Groq.APIKey = ""
	}
	if p.Mistral != nil {
		p.Mistral.APIKey = ""
	}
	if p.Together != nil {
		p.Together.APIKey = ""
	}
	if p.OpenRouter != nil {
		p.OpenRouter.APIKey = ""
	}
	if p.NVIDIA != nil {
		p.NVIDIA.APIKey = ""
	}
	if p.Cohere != nil {
		p.Cohere.APIKey = ""
	}
	if p.DeepSeek != nil {
		p.DeepSeek.APIKey = ""
	}
	if p.Perplexity != nil {
		p.Perplexity.APIKey = ""
	}
	if p.XAI != nil {
		p.XAI.APIKey = ""
	}
	if p.Voyage != nil {
		p.Voyage.APIKey = ""
	}
	if p.HuggingFace != nil {
		p.HuggingFace.APIKey = ""
	}
	if p.Bedrock != nil {
		p.Bedrock.APIKey = ""
		p.Bedrock.SecretAccessKey = ""
	}
	if p.Vertex != nil {
		p.Vertex.Credentials = ""
	}
	if p.Ollama != nil {
		p.Ollama.APIKey = ""
	}
	if p.VLLM != nil {
		p.VLLM.APIKey = ""
	}
	if p.LMStudio != nil {
		p.LMStudio.APIKey = ""
	}
}
