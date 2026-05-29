// Package cost tracks per-run, per-card, and per-session LLM costs.
package cost

import "sync"

// ModelPricing holds per-1M-token pricing in USD.
type ModelPricing struct {
	InputPer1M  float64
	OutputPer1M float64
}

// Default pricing table. Override via Config() for custom providers.
var defaultPricing = map[string]ModelPricing{
	// Anthropic
	"claude-opus-4":     {InputPer1M: 15.00, OutputPer1M: 75.00},
	"claude-sonnet-4":   {InputPer1M: 3.00, OutputPer1M: 15.00},
	"claude-haiku-4":    {InputPer1M: 0.25, OutputPer1M: 1.25},
	"claude-3-opus":     {InputPer1M: 15.00, OutputPer1M: 75.00},
	"claude-3-sonnet":   {InputPer1M: 3.00, OutputPer1M: 15.00},
	"claude-3-haiku":    {InputPer1M: 0.25, OutputPer1M: 1.25},
	// OpenAI
	"gpt-4o":            {InputPer1M: 2.50, OutputPer1M: 10.00},
	"gpt-4o-mini":       {InputPer1M: 0.15, OutputPer1M: 0.60},
	"gpt-4-turbo":       {InputPer1M: 10.00, OutputPer1M: 30.00},
	"gpt-4":             {InputPer1M: 30.00, OutputPer1M: 60.00},
	"gpt-3.5-turbo":     {InputPer1M: 0.50, OutputPer1M: 1.50},
	// Google
	"gemini-1.5-pro":    {InputPer1M: 3.50, OutputPer1M: 10.50},
	"gemini-1.5-flash":  {InputPer1M: 0.35, OutputPer1M: 0.53},
	// Local / free
	"gemma-2-2b":        {InputPer1M: 0.00, OutputPer1M: 0.00},
	"llama3":            {InputPer1M: 0.00, OutputPer1M: 0.00},
}

// Calculator holds pricing and computes costs.
type Calculator struct {
	mu      sync.RWMutex
	pricing map[string]ModelPricing
}

// NewCalculator creates a calculator with default pricing.
func NewCalculator() *Calculator {
	c := &Calculator{pricing: make(map[string]ModelPricing)}
	for k, v := range defaultPricing {
		c.pricing[k] = v
	}
	return c
}

// SetPricing overrides pricing for a model.
func (c *Calculator) SetPricing(model string, p ModelPricing) {
	c.mu.Lock()
	c.pricing[model] = p
	c.mu.Unlock()
}

// Calculate returns the cost in USD for a given model and token counts.
func (c *Calculator) Calculate(model string, tokensIn, tokensOut int64) float64 {
	c.mu.RLock()
	p, ok := c.pricing[model]
	c.mu.RUnlock()
	if !ok {
		// Try prefix matching for versioned models (e.g., "claude-sonnet-4.7" → "claude-sonnet-4")
		p = c.matchPrefix(model)
	}
	inCost := float64(tokensIn) * p.InputPer1M / 1e6
	outCost := float64(tokensOut) * p.OutputPer1M / 1e6
	return inCost + outCost
}

func (c *Calculator) matchPrefix(model string) ModelPricing {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Try progressively shorter prefixes
	for i := len(model); i > 0; i-- {
		if p, ok := c.pricing[model[:i]]; ok {
			return p
		}
	}
	return ModelPricing{} // free if unknown
}

// SupportedModels returns the list of models with known pricing.
func (c *Calculator) SupportedModels() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.pricing))
	for k := range c.pricing {
		out = append(out, k)
	}
	return out
}
