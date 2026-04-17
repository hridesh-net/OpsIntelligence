package prompts

import (
	"fmt"
	"sort"
	"sync"
)

// Library holds the in-memory catalogue of SmartPrompts and Chains.
// Writes are serialised; reads are RLocked.
type Library struct {
	mu      sync.RWMutex
	prompts map[string]*SmartPrompt
	chains  map[string]*Chain
}

// NewLibrary returns an empty Library.
func NewLibrary() *Library {
	return &Library{
		prompts: make(map[string]*SmartPrompt),
		chains:  make(map[string]*Chain),
	}
}

// AddPrompt inserts or replaces a SmartPrompt. Returns an error when the
// prompt is nil or has no ID.
func (l *Library) AddPrompt(p *SmartPrompt) error {
	if p == nil {
		return fmt.Errorf("prompts: nil SmartPrompt")
	}
	if p.ID == "" {
		return fmt.Errorf("prompts: missing id (source=%s)", p.SourcePath)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prompts[p.ID] = p
	return nil
}

// AddChain inserts or replaces a Chain. Returns an error when the chain
// references unknown prompts (fail-fast at load time).
func (l *Library) AddChain(c *Chain) error {
	if c == nil {
		return fmt.Errorf("prompts: nil Chain")
	}
	if c.ID == "" {
		return fmt.Errorf("prompts: chain missing id (source=%s)", c.SourcePath)
	}
	if len(c.Steps) == 0 {
		return fmt.Errorf("prompts: chain %q has no steps", c.ID)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, step := range c.Steps {
		if _, ok := l.prompts[step]; !ok {
			return fmt.Errorf("prompts: chain %q references unknown prompt %q", c.ID, step)
		}
	}
	l.chains[c.ID] = c
	return nil
}

// Prompt returns a SmartPrompt by ID.
func (l *Library) Prompt(id string) (*SmartPrompt, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p, ok := l.prompts[id]
	return p, ok
}

// Chain returns a Chain by ID.
func (l *Library) Chain(id string) (*Chain, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	c, ok := l.chains[id]
	return c, ok
}

// ListPrompts returns all prompts sorted by ID.
func (l *Library) ListPrompts() []*SmartPrompt {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]*SmartPrompt, 0, len(l.prompts))
	for _, p := range l.prompts {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListChains returns all chains sorted by ID.
func (l *Library) ListChains() []*Chain {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]*Chain, 0, len(l.chains))
	for _, c := range l.chains {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Index renders a compact one-line-per-chain listing for injection into
// the agent's system prompt. It lets the model pick a chain without
// needing to read every prompt body.
//
// Example output:
//
//	Smart Prompts:
//	  - chain:pr-review       End-to-end PR/MR review (gather→analyze→critique→render)
//	  - chain:sonar-triage    SonarQube triage (fetch→classify→recommend)
//	  - prompt:self-critique  Reflect on a draft and flag missing evidence
func (l *Library) Index() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.chains) == 0 && len(l.prompts) == 0 {
		return ""
	}
	var sb stringBuilder
	sb.WriteString("Smart Prompts:\n")
	chainIDs := make([]string, 0, len(l.chains))
	for id := range l.chains {
		chainIDs = append(chainIDs, id)
	}
	sort.Strings(chainIDs)
	for _, id := range chainIDs {
		c := l.chains[id]
		sb.WriteString(fmt.Sprintf("  - chain:%-20s %s\n", id, c.Purpose))
	}
	promptIDs := make([]string, 0, len(l.prompts))
	for id, p := range l.prompts {
		if len(p.Tags) > 0 && containsTag(p.Tags, "meta") {
			promptIDs = append(promptIDs, id)
		}
	}
	sort.Strings(promptIDs)
	for _, id := range promptIDs {
		p := l.prompts[id]
		sb.WriteString(fmt.Sprintf("  - prompt:%-19s %s\n", id, p.Purpose))
	}
	return sb.String()
}

// stringBuilder is a local minimal wrapper so we don't pull strings just
// for the Index helper; keeps the zero-value safe.
type stringBuilder struct{ buf []byte }

func (s *stringBuilder) WriteString(v string) { s.buf = append(s.buf, v...) }
func (s *stringBuilder) String() string       { return string(s.buf) }

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
