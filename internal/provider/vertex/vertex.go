// Package vertex implements the Google Vertex AI provider for OpsIntelligence.
//
// It supports the Gemini model family through the Vertex AI generateContent
// and streamGenerateContent endpoints, including native function calling,
// vision, and streaming.
package vertex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"os"

	"github.com/opsintelligence/opsintelligence/internal/provider"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	providerName   = "vertex"
	defaultTimeout = 120 * time.Second
)

// Config holds settings for Vertex AI.
type Config struct {
	ProjectID    string `yaml:"project_id"`
	Location     string `yaml:"location"`
	Credentials  string `yaml:"credentials"` // path to service account JSON
	DefaultModel string `yaml:"default_model"`
}

// Provider implements provider.Provider for Vertex AI.
type Provider struct {
	cfg    Config
	client *http.Client
	ts     oauth2.TokenSource
}

// New creates a new Vertex AI provider.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	if cfg.Location == "" {
		cfg.Location = "us-central1"
	}

	var ts oauth2.TokenSource

	if cfg.Credentials != "" {
		data, err := os.ReadFile(cfg.Credentials)
		if err != nil {
			return nil, fmt.Errorf("vertex: read credentials: %w", err)
		}
		creds, err := google.CredentialsFromJSON(ctx, data, "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return nil, fmt.Errorf("vertex: parse credentials: %w", err)
		}
		ts = creds.TokenSource
	} else {
		// Try default credentials
		creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err == nil {
			ts = creds.TokenSource
		}
	}

	return &Provider{
		cfg:    cfg,
		client: &http.Client{Timeout: defaultTimeout},
		ts:     ts,
	}, nil
}

func (p *Provider) Name() string { return providerName }

func (p *Provider) HealthCheck(ctx context.Context) error {
	return p.ValidateModel(ctx, p.cfg.DefaultModel)
}

func (p *Provider) ValidateModel(ctx context.Context, modelID string) error {
	if modelID == "" {
		modelID = p.cfg.DefaultModel
	}
	if modelID == "" {
		return nil
	}

	models, _ := p.ListModels(ctx)
	for _, m := range models {
		if m.ID == modelID {
			return nil
		}
	}
	return &provider.ProviderError{
		Provider:   providerName,
		StatusCode: http.StatusNotFound,
		Message:    fmt.Sprintf("model %q not found", modelID),
	}
}

func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{
		{
			ID: "gemini-2.0-flash-exp", Name: "Gemini 2.0 Flash Exp", Provider: providerName,
			Capabilities:    []provider.Capability{provider.CapabilityVision, provider.CapabilityTools, provider.CapabilityStreaming},
			ContextWindow:   1000000,
			MaxOutputTokens: 8192,
		},
		{
			ID: "gemini-2.0-flash-thinking-exp-01-21", Name: "Gemini 2.0 Flash Thinking", Provider: providerName,
			Capabilities:    []provider.Capability{provider.CapabilityReasoning, provider.CapabilityVision, provider.CapabilityTools, provider.CapabilityStreaming},
			ContextWindow:   1000000,
			MaxOutputTokens: 8192,
		},
		{
			ID: "gemini-2.0-flash", Name: "Gemini 2.0 Flash", Provider: providerName,
			Capabilities:    []provider.Capability{provider.CapabilityVision, provider.CapabilityTools, provider.CapabilityStreaming},
			ContextWindow:   1000000,
			MaxOutputTokens: 8192,
		},
		{
			ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Provider: providerName,
			Capabilities:    []provider.Capability{provider.CapabilityVision, provider.CapabilityTools, provider.CapabilityStreaming},
			ContextWindow:   2000000,
			MaxOutputTokens: 8192,
		},
		{
			ID: "gemini-1.5-pro", Name: "Gemini 1.5 Pro", Provider: providerName,
			Capabilities:    []provider.Capability{provider.CapabilityVision, provider.CapabilityTools, provider.CapabilityStreaming},
			ContextWindow:   2000000,
			MaxOutputTokens: 8192,
		},
		{
			ID: "gemini-1.5-flash", Name: "Gemini 1.5 Flash", Provider: providerName,
			Capabilities:    []provider.Capability{provider.CapabilityVision, provider.CapabilityTools, provider.CapabilityStreaming},
			ContextWindow:   1000000,
			MaxOutputTokens: 8192,
		},
	}, nil
}

func (p *Provider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	start := time.Now()
	model := req.Model
	if model == "" {
		model = p.cfg.DefaultModel
	}
	if model == "" {
		model = "gemini-1.5-flash"
	}

	url := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent",
		p.cfg.Location, p.cfg.ProjectID, p.cfg.Location, model)

	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	token, err := p.ts.Token()
	if err != nil {
		return nil, fmt.Errorf("vertex: get token: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		var errData any
		_ = json.NewDecoder(httpResp.Body).Decode(&errData)
		return nil, fmt.Errorf("vertex: http %d: %v", httpResp.StatusCode, errData)
	}

	return p.parseResponse(httpResp.Body, model, start)
}

func (p *Provider) Stream(ctx context.Context, req *provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = p.cfg.DefaultModel
	}
	if model == "" {
		model = "gemini-1.5-flash"
	}

	url := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:streamGenerateContent?alt=sse",
		p.cfg.Location, p.cfg.ProjectID, p.cfg.Location, model)

	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	token, err := p.ts.Token()
	if err != nil {
		return nil, fmt.Errorf("vertex: get token: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		var errData any
		_ = json.NewDecoder(httpResp.Body).Decode(&errData)
		return nil, fmt.Errorf("vertex: http %d: %v", httpResp.StatusCode, errData)
	}

	ch := make(chan provider.StreamEvent, 16)
	go p.readSSE(httpResp.Body, ch)
	return ch, nil
}

func (p *Provider) Embed(ctx context.Context, model string, text string) ([]float32, error) {
	if model == "" {
		model = p.cfg.DefaultModel
	}
	if model == "" {
		model = "text-embedding-004"
	}

	url := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:predict",
		p.cfg.Location, p.cfg.ProjectID, p.cfg.Location, model)

	payload := map[string]any{
		"instances": []map[string]any{
			{"content": text},
		},
	}
	body, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	token, err := p.ts.Token()
	if err != nil {
		return nil, fmt.Errorf("vertex: get token: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		var errData any
		_ = json.NewDecoder(httpResp.Body).Decode(&errData)
		return nil, fmt.Errorf("vertex: http %d: %v", httpResp.StatusCode, errData)
	}

	var resp struct {
		Predictions []struct {
			Embeddings struct {
				Values []float32 `json:"values"`
			} `json:"embeddings"`
		} `json:"predictions"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, err
	}
	if len(resp.Predictions) == 0 {
		return nil, fmt.Errorf("vertex: no embedding predictions")
	}
	return resp.Predictions[0].Embeddings.Values, nil
}

// ── request builder ──

func (p *Provider) buildRequestBody(req *provider.CompletionRequest) ([]byte, error) {
	type part struct {
		Text           string `json:"text,omitempty"`
		InlineData     any    `json:"inlineData,omitempty"`
		FunctionCall   any    `json:"functionCall,omitempty"`
		FunctionResponse any `json:"functionResponse,omitempty"`
	}
	type content struct {
		Role  string `json:"role"`
		Parts []part `json:"parts"`
	}
	type functionDeclaration struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	}
	type tool struct {
		FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
	}
	type generationConfig struct {
		Temperature    float64 `json:"temperature,omitempty"`
		MaxOutputTokens int    `json:"maxOutputTokens,omitempty"`
		TopP           float64 `json:"topP,omitempty"`
	}
	type request struct {
		Contents         []content          `json:"contents"`
		SystemInstruction *content          `json:"systemInstruction,omitempty"`
		Tools            []tool             `json:"tools,omitempty"`
		ToolConfig       map[string]any     `json:"toolConfig,omitempty"`
		GenerationConfig *generationConfig  `json:"generationConfig,omitempty"`
	}

	var vertexReq request
	if req.SystemPrompt != "" {
		vertexReq.SystemInstruction = &content{
			Parts: []part{{Text: req.SystemPrompt}},
		}
	}

	// Tools
	if len(req.Tools) > 0 {
		decls := make([]functionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, functionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schemaToGemini(t.InputSchema),
			})
		}
		vertexReq.Tools = []tool{{FunctionDeclarations: decls}}
		vertexReq.ToolConfig = map[string]any{
			"functionCallingConfig": map[string]any{"mode": "AUTO"},
		}
	}

	// Generation config
	if req.Temperature > 0 || req.MaxTokens > 0 || req.TopP > 0 {
		vertexReq.GenerationConfig = &generationConfig{}
		if req.Temperature > 0 {
			vertexReq.GenerationConfig.Temperature = req.Temperature
		}
		if req.MaxTokens > 0 {
			vertexReq.GenerationConfig.MaxOutputTokens = req.MaxTokens
		}
		if req.TopP > 0 {
			vertexReq.GenerationConfig.TopP = req.TopP
		}
	}

	// Messages
	for _, m := range req.Messages {
		role := string(m.Role)
		switch m.Role {
		case provider.RoleAssistant:
			role = "model"
		case provider.RoleTool:
			// Gemini doesn't have a "tool" role; tool results go in user messages with functionResponse parts
			role = "user"
		}

		var parts []part
		for _, cp := range m.Content {
			switch cp.Type {
			case provider.ContentTypeText:
				parts = append(parts, part{Text: cp.Text})
			case provider.ContentTypeImage:
				if cp.ImageData != nil {
					parts = append(parts, part{InlineData: map[string]any{
						"mimeType": cp.ImageMimeType,
						"data":     cp.ImageData,
					}})
				} else if cp.ImageURL != "" {
					parts = append(parts, part{Text: fmt.Sprintf("[Image: %s]", cp.ImageURL)})
				}
			case provider.ContentTypeToolUse:
				parts = append(parts, part{FunctionCall: map[string]any{
					"name": cp.ToolName,
					"args": cp.ToolInput,
				}})
			case provider.ContentTypeToolResult:
				resp := map[string]any{"result": cp.ToolResultContent}
				if cp.ToolResultError {
					resp = map[string]any{"error": cp.ToolResultContent}
				}
				parts = append(parts, part{FunctionResponse: map[string]any{
					"name":     m.Name,
					"response": resp,
				}})
			}
		}
		if len(parts) > 0 {
			vertexReq.Contents = append(vertexReq.Contents, content{Role: role, Parts: parts})
		}
	}

	return json.Marshal(vertexReq)
}

func schemaToGemini(tp provider.ToolParameter) any {
	// Gemini uses OpenAPI-like schema; map our ToolParameter to it.
	out := map[string]any{
		"type":        tp.Type,
		"description": tp.Description,
	}
	if len(tp.Enum) > 0 {
		out["enum"] = tp.Enum
	}
	if len(tp.Properties) > 0 {
		props := make(map[string]any)
		for k, v := range tp.Properties {
			if sub, ok := v.(provider.ToolParameter); ok {
				props[k] = schemaToGemini(sub)
			} else if subMap, ok := v.(map[string]any); ok {
				props[k] = subMap
			} else {
				props[k] = v
			}
		}
		out["properties"] = props
	}
	if len(tp.Required) > 0 {
		out["required"] = tp.Required
	}
	if tp.Items != nil {
		out["items"] = schemaToGemini(*tp.Items)
	}
	return out
}

// ── response parser ──

func (p *Provider) parseResponse(body io.Reader, model string, start time.Time) (*provider.CompletionResponse, error) {
	var vertexResp struct {
		Candidates []struct {
			Content struct {
				Role  string `json:"role"`
				Parts []struct {
					Text           string         `json:"text"`
					FunctionCall   map[string]any `json:"functionCall"`
					FunctionResponse map[string]any `json:"functionResponse"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}

	if err := json.NewDecoder(body).Decode(&vertexResp); err != nil {
		return nil, err
	}

	resp := &provider.CompletionResponse{
		Model:   model,
		Latency: time.Since(start),
		Usage: provider.TokenUsage{
			PromptTokens:     vertexResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: vertexResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      vertexResp.UsageMetadata.TotalTokenCount,
		},
	}

	if len(vertexResp.Candidates) > 0 {
		c := vertexResp.Candidates[0]
		for _, part := range c.Content.Parts {
			if part.Text != "" {
				resp.Content = append(resp.Content, provider.ContentPart{
					Type: provider.ContentTypeText,
					Text: part.Text,
				})
			}
			if part.FunctionCall != nil {
				name, _ := part.FunctionCall["name"].(string)
				args := part.FunctionCall["args"]
				resp.Content = append(resp.Content, provider.ContentPart{
					Type:      provider.ContentTypeToolUse,
					ToolName:  name,
					ToolInput: args,
				})
				resp.FinishReason = provider.FinishReasonToolUse
			}
		}
		if resp.FinishReason == "" {
			resp.FinishReason = provider.FinishReason(strings.ToLower(c.FinishReason))
		}
	}

	return resp, nil
}

// ── SSE streaming ──

func (p *Provider) readSSE(r io.ReadCloser, ch chan<- provider.StreamEvent) {
	defer close(ch)
	defer r.Close()

	scanner := bufio.NewScanner(r)
	var textBuf string
	var toolCall *provider.ContentPart
	var finishReason provider.FinishReason

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		var chunk struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text         string         `json:"text"`
						FunctionCall map[string]any `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				TotalTokenCount      int `json:"totalTokenCount"`
			} `json:"usageMetadata"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			ch <- provider.StreamEvent{Type: provider.StreamEventError, Err: err}
			return
		}

		if len(chunk.Candidates) > 0 {
			c := chunk.Candidates[0]
			for _, part := range c.Content.Parts {
				if part.Text != "" {
					textBuf += part.Text
					ch <- provider.StreamEvent{Type: provider.StreamEventText, Text: part.Text}
				}
				if part.FunctionCall != nil {
					name, _ := part.FunctionCall["name"].(string)
					args := part.FunctionCall["args"]
					toolCall = &provider.ContentPart{
						Type:      provider.ContentTypeToolUse,
						ToolName:  name,
						ToolInput: args,
					}
					ch <- provider.StreamEvent{Type: provider.StreamEventToolUse, ToolUse: toolCall}
				}
			}
			if c.FinishReason != "" {
				finishReason = provider.FinishReason(strings.ToLower(c.FinishReason))
			}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- provider.StreamEvent{Type: provider.StreamEventError, Err: err}
		return
	}

	ch <- provider.StreamEvent{Type: provider.StreamEventDone, FinishReason: finishReason}
}
