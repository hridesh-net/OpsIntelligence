package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/opsintelligence/opsintelligence/internal/provider"
	"go.uber.org/zap"
)

// ── Circuit breaker ───────────────────────────────────────────────────────────

const (
	cbThreshold  = 5                // consecutive errors to open
	cbHalfOpen   = 30 * time.Second // time before trying again
)

// circuitBreaker is a simple per-provider fault detector.
// States: closed (normal) → open (errors) → half-open (probe) → closed.
type circuitBreaker struct {
	consecutive int64     // consecutive error count (atomic)
	openAt      time.Time // when the breaker tripped
	mu          sync.Mutex
}

func (cb *circuitBreaker) allow() bool {
	n := atomic.LoadInt64(&cb.consecutive)
	if n < cbThreshold {
		return true
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if time.Since(cb.openAt) >= cbHalfOpen {
		// Allow one probe request through (half-open state).
		return true
	}
	return false
}

func (cb *circuitBreaker) success() {
	atomic.StoreInt64(&cb.consecutive, 0)
}

func (cb *circuitBreaker) failure() {
	n := atomic.AddInt64(&cb.consecutive, 1)
	if n == cbThreshold {
		cb.mu.Lock()
		cb.openAt = time.Now()
		cb.mu.Unlock()
	}
}

// ── Provider slot ─────────────────────────────────────────────────────────────

// providerSlot bundles a provider with its rate limiter and circuit breaker.
type providerSlot struct {
	prov      provider.Provider
	model     string
	isLocal   bool
	limiter   *rate.Limiter // nil means unlimited (local intel)
	cb        *circuitBreaker
}

// newProviderSlot creates a slot with a token-bucket refilling at rpm/60 tokens/sec.
// rpm ≤ 0 means unlimited (no rate limiting applied).
func newProviderSlot(prov provider.Provider, model string, isLocal bool, rpm int, burst float64) *providerSlot {
	s := &providerSlot{
		prov:    prov,
		model:   model,
		isLocal: isLocal,
		cb:      &circuitBreaker{},
	}
	if rpm > 0 {
		r := rate.Limit(float64(rpm) / 60.0) // tokens per second
		b := int(float64(rpm) * burst / 60.0)
		if b < 1 {
			b = 1
		}
		s.limiter = rate.NewLimiter(r, b)
	}
	return s
}

// acquire waits until the rate limiter allows a request, or ctx is cancelled.
// Returns immediately for unlimited slots.
func (s *providerSlot) acquire(ctx context.Context) error {
	if s.limiter == nil {
		return nil
	}
	return s.limiter.Wait(ctx)
}

// ── LLM Router ───────────────────────────────────────────────────────────────

// RouteResult is returned by LLMRouter.Route and describes which provider
// was selected for a given request.
type RouteResult struct {
	Provider   provider.Provider
	Model      string
	IsLocal    bool // true when routed to local intel
	ProviderID string // "primary" | "secondary" | "local"
}

// LLMRouter selects a provider for each LLM call using:
//  1. ComplexityClassifier — small/non-sensitive diffs → local intel
//  2. Token-bucket rate limiter per provider — no artificial cap beyond RPM
//  3. Overflow routing: primary saturated/tripped → secondary → local intel
//  4. Circuit breaker per provider — 5 consecutive errors → open for 30s
//
// Local intel (when available) has no rate limit — it runs fully in-process
// and handles as many concurrent calls as CPU allows.
type LLMRouter struct {
	primary    *providerSlot // required
	secondary  *providerSlot // optional, nil if not configured
	local      *providerSlot // optional, nil if local intel disabled
	classifier *ComplexityClassifier
	log        *zap.Logger
}

// LLMRouterConfig configures the LLMRouter.
type LLMRouterConfig struct {
	Primary   provider.Provider
	PrimaryModel string
	PrimaryRPM   int     // requests per minute; 0 = 60
	BurstMult    float64 // burst multiplier for token bucket; 0 = 1.5

	Secondary   provider.Provider // optional
	SecondaryModel string
	SecondaryRPM int // 0 = disabled (unlimited if provider present)

	LocalIntel  provider.Provider // optional, nil if local intel disabled
	LocalIntelModel string

	LocalIntelDiffMax int // max diff lines for local intel routing; 0 = 200
}

// NewLLMRouter constructs a router. primary must not be nil.
func NewLLMRouter(cfg LLMRouterConfig, log *zap.Logger) (*LLMRouter, error) {
	if cfg.Primary == nil {
		return nil, fmt.Errorf("llm router: primary provider is required")
	}
	if cfg.PrimaryRPM <= 0 {
		cfg.PrimaryRPM = 60
	}
	if cfg.BurstMult <= 0 {
		cfg.BurstMult = 1.5
	}

	r := &LLMRouter{
		primary:    newProviderSlot(cfg.Primary, cfg.PrimaryModel, false, cfg.PrimaryRPM, cfg.BurstMult),
		classifier: NewComplexityClassifier(cfg.LocalIntelDiffMax),
		log:        log,
	}
	if cfg.Secondary != nil {
		secondaryRPM := cfg.SecondaryRPM
		r.secondary = newProviderSlot(cfg.Secondary, cfg.SecondaryModel, false, secondaryRPM, cfg.BurstMult)
	}
	if cfg.LocalIntel != nil {
		// Local intel is unlimited — no rate limiting, no circuit breaker needed.
		r.local = newProviderSlot(cfg.LocalIntel, cfg.LocalIntelModel, true, 0, 0)
	}
	return r, nil
}

// Route selects the best available provider for a request.
//
// Selection order:
//  1. If diff is low-complexity AND local intel is available → local intel (no wait)
//  2. If primary circuit is closed → wait for primary token bucket
//  3. If primary circuit is open AND secondary available → wait for secondary bucket
//  4. If both remote providers unavailable → local intel (fallback, if available)
//
// Route blocks until a rate-limit token is acquired or ctx is cancelled.
func (r *LLMRouter) Route(ctx context.Context, diffLines int, filePaths []string) (RouteResult, error) {
	complexity := r.classifier.Classify(diffLines, filePaths)

	// Fast path: low complexity → local intel (no rate limit wait)
	if complexity == ComplexityLow && r.local != nil && r.local.cb.allow() {
		if r.log != nil {
			r.log.Debug("llm router: routed to local intel",
				zap.Int("diff_lines", diffLines),
			)
		}
		return RouteResult{
			Provider: r.local.prov, Model: r.local.model,
			IsLocal: true, ProviderID: "local",
		}, nil
	}

	// Try primary.
	if r.primary.cb.allow() {
		if err := r.primary.acquire(ctx); err != nil {
			return RouteResult{}, fmt.Errorf("llm router: primary rate limit: %w", err)
		}
		if r.log != nil {
			r.log.Debug("llm router: routed to primary",
				zap.Int("diff_lines", diffLines),
				zap.String("complexity", complexityString(complexity)),
			)
		}
		return RouteResult{
			Provider: r.primary.prov, Model: r.primary.model,
			IsLocal: false, ProviderID: "primary",
		}, nil
	}

	// Primary circuit open — try secondary.
	if r.secondary != nil && r.secondary.cb.allow() {
		if err := r.secondary.acquire(ctx); err == nil {
			if r.log != nil {
				r.log.Warn("llm router: primary circuit open, using secondary")
			}
			return RouteResult{
				Provider: r.secondary.prov, Model: r.secondary.model,
				IsLocal: false, ProviderID: "secondary",
			}, nil
		}
	}

	// Both remote providers unavailable — fall back to local intel.
	if r.local != nil {
		if r.log != nil {
			r.log.Warn("llm router: all remote providers unavailable, falling back to local intel")
		}
		return RouteResult{
			Provider: r.local.prov, Model: r.local.model,
			IsLocal: true, ProviderID: "local",
		}, nil
	}

	return RouteResult{}, fmt.Errorf("llm router: no provider available (primary circuit open, no secondary or local intel)")
}

// RecordSuccess notifies the router that a call to the given provider succeeded.
func (r *LLMRouter) RecordSuccess(providerID string) {
	switch providerID {
	case "primary":
		r.primary.cb.success()
	case "secondary":
		if r.secondary != nil {
			r.secondary.cb.success()
		}
	case "local":
		if r.local != nil {
			r.local.cb.success()
		}
	}
}

// RecordFailure notifies the router that a call to the given provider failed.
func (r *LLMRouter) RecordFailure(providerID string) {
	switch providerID {
	case "primary":
		r.primary.cb.failure()
	case "secondary":
		if r.secondary != nil {
			r.secondary.cb.failure()
		}
	case "local":
		if r.local != nil {
			r.local.cb.failure()
		}
	}
	if r.log != nil {
		r.log.Warn("llm router: provider failure recorded",
			zap.String("provider", providerID),
			zap.Int64("consecutive", atomic.LoadInt64(&r.slotByID(providerID).cb.consecutive)),
		)
	}
}

func (r *LLMRouter) slotByID(id string) *providerSlot {
	switch id {
	case "secondary":
		if r.secondary != nil {
			return r.secondary
		}
	case "local":
		if r.local != nil {
			return r.local
		}
	}
	return r.primary
}

func complexityString(c ComplexityLevel) string {
	if c == ComplexityLow {
		return "low"
	}
	return "high"
}
