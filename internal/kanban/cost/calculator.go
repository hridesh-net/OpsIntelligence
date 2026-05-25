// Package cost calculates per-model token pricing and roll-up costs for kanban runs.
package cost

import (
	"sync"
)

// PricingTable maps model identifiers to per-million-token costs in USD.
type PricingTable struct {
	mu     sync.RWMutex
	prices map[string]Price // key: "provider/model" or just "model"
}

// Price is the cost per million tokens.
type Price struct {
	InputCostPerM  float64
	OutputCostPerM float64
}

// NewPricingTable creates a table pre-seeded with known model prices.
func NewPricingTable() *PricingTable {
	t := &PricingTable{prices: make(map[string]Price)}
	t.seedDefaults()
	return t
}

// CostUSD returns the dollar cost for the given token counts.
func (t *PricingTable) CostUSD(model string, tokensIn, tokensOut int64) float64 {
	t.mu.RLock()
	p, ok := t.prices[model]
	if !ok {
		// Try fallback without provider prefix.
		for k, v := range t.prices {
			if stripProvider(k) == model {
				p = v
				ok = true
				break
			}
		}
	}
	t.mu.RUnlock()

	if !ok {
		return 0
	}
	inCost := float64(tokensIn) * p.InputCostPerM / 1e6
	outCost := float64(tokensOut) * p.OutputCostPerM / 1e6
	return inCost + outCost
}

// SetPrice overrides or adds a price for a model.
func (t *PricingTable) SetPrice(model string, inputCostPerM, outputCostPerM float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prices[model] = Price{InputCostPerM: inputCostPerM, OutputCostPerM: outputCostPerM}
}

func (t *PricingTable) seedDefaults() {
	defaults := map[string]Price{
		"gemini-2.5-flash":        {0.15, 0.60},
		"gemini-2.5-pro":          {1.25, 10.00},
		"gemini-2.0-flash":        {0.10, 0.40},
		"gemini-1.5-flash":        {0.075, 0.30},
		"gemini-1.5-pro":          {1.25, 5.00},
		"gpt-4o":                  {2.50, 10.00},
		"gpt-4o-mini":             {0.15, 0.60},
		"gpt-4-turbo":             {10.00, 30.00},
		"claude-3-5-sonnet":       {3.00, 15.00},
		"claude-3-5-haiku":        {0.80, 4.00},
		"claude-3-opus":           {15.00, 75.00},
		"deepseek-chat":           {0.27, 1.10},
		"deepseek-reasoner":       {0.55, 2.19},
		"openai/gpt-4o":           {2.50, 10.00},
		"anthropic/claude-3-5-sonnet": {3.00, 15.00},
		"google/gemini-2.5-flash": {0.15, 0.60},
	}
	for k, v := range defaults {
		t.prices[k] = v
	}
}

func stripProvider(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}
