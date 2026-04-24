package pipeline

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// traceAgentBufSize is the channel buffer. Stages drop events rather than block
// if the agent falls behind — pipeline throughput is never sacrificed for tracing.
const traceAgentBufSize = 512

// inFlightTrace accumulates StageEvents until all stages have reported.
type inFlightTrace struct {
	trace    *PipelineTrace
	received map[string]bool // stage name → received
}

func newInFlight(e StageEvent) *inFlightTrace {
	t := &PipelineTrace{
		RunID:     e.RunID,
		PRURL:     e.PRURL,
		PRNumber:  e.PRNumber,
		Repo:      e.Repo,
		CommitSHA: e.CommitSHA,
		StartedAt: e.StartedAt,
	}
	return &inFlightTrace{
		trace:    t,
		received: make(map[string]bool),
	}
}

func (f *inFlightTrace) apply(e StageEvent) {
	rec := StageRecord{
		Name:       e.Stage,
		StartedAt:  e.StartedAt,
		DurationMs: e.DurationMs,
		Success:    e.Success,
		Output:     e.Output,
		Error:      e.Error,
	}
	f.trace.Stages = append(f.trace.Stages, rec)
	f.received[e.Stage] = true

	// Carry over per-stage metadata into the top-level trace.
	if e.Stage == StageReviewName {
		f.trace.ModelUsed = e.ModelUsed
		f.trace.LocalIntel = e.LocalIntel
		f.trace.ToolsInvoked = e.Tools
		f.trace.SkillsUsed = e.Skills
		f.trace.Tokens = e.Tokens
	}
	if e.Stage == StagePostName {
		f.trace.Verdict = e.Verdict
		f.trace.InlineCount = e.InlineCount
		f.trace.SandboxPass = e.SandboxPass
	}
	if e.Stage == StageFetchName && f.trace.CommitSHA == "" {
		f.trace.CommitSHA = e.CommitSHA
	}
	if !e.Success && f.trace.Error == "" {
		f.trace.Error = e.Error
	}
}

func (f *inFlightTrace) complete() bool {
	for _, s := range stageOrder {
		if !f.received[s] {
			return false
		}
	}
	return true
}

// TraceAgent collects StageEvents from all pipeline workers and persists
// completed PipelineTraces to a TraceStore. It runs as a single background
// goroutine and never blocks the pipeline stages (events are dropped if the
// buffer is full, which only happens under extreme backlog).
type TraceAgent struct {
	ch    chan StageEvent
	store TraceStore
	log   *zap.Logger

	// runs holds in-flight traces keyed by runID.
	runs map[string]*inFlightTrace
	mu   sync.Mutex // guards runs map (only the agent goroutine writes, but
	//                 Snapshot reads from outside)
}

// NewTraceAgent constructs a TraceAgent. Call Run in a goroutine.
func NewTraceAgent(store TraceStore, log *zap.Logger) *TraceAgent {
	if store == nil {
		store = NopTraceStore{}
	}
	return &TraceAgent{
		ch:    make(chan StageEvent, traceAgentBufSize),
		store: store,
		log:   log,
		runs:  make(map[string]*inFlightTrace),
	}
}

// Emit sends a StageEvent to the agent. Non-blocking: if the buffer is full
// the event is dropped and a warning is logged.
func (a *TraceAgent) Emit(e StageEvent) {
	select {
	case a.ch <- e:
	default:
		if a.log != nil {
			a.log.Warn("trace agent buffer full, dropping event",
				zap.String("run_id", e.RunID),
				zap.String("stage", e.Stage),
			)
		}
	}
}

// Run processes events until ctx is cancelled. Call as a goroutine.
func (a *TraceAgent) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// Drain remaining events before exit.
			a.drain()
			return
		case e := <-a.ch:
			a.handle(e)
		}
	}
}

func (a *TraceAgent) handle(e StageEvent) {
	a.mu.Lock()
	inf, ok := a.runs[e.RunID]
	if !ok {
		inf = newInFlight(e)
		a.runs[e.RunID] = inf
	}
	inf.apply(e)
	done := inf.complete()
	if done {
		delete(a.runs, e.RunID)
	}
	a.mu.Unlock()

	if done {
		t := inf.trace
		t.CompletedAt = time.Now()
		t.DurationMs = t.CompletedAt.Sub(t.StartedAt).Milliseconds()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.store.Save(ctx, t); err != nil && a.log != nil {
			a.log.Error("trace store save failed",
				zap.String("run_id", t.RunID),
				zap.Error(err),
			)
		} else if a.log != nil {
			a.log.Info("pipeline trace persisted",
				zap.String("run_id", t.RunID),
				zap.String("repo", t.Repo),
				zap.Int("pr", t.PRNumber),
				zap.String("verdict", t.Verdict),
				zap.Int64("duration_ms", t.DurationMs),
				zap.Bool("local_intel", t.LocalIntel),
			)
		}
	}
}

func (a *TraceAgent) drain() {
	for {
		select {
		case e := <-a.ch:
			a.handle(e)
		default:
			return
		}
	}
}

// Snapshot returns a copy of all currently in-flight trace stubs.
// Safe to call from any goroutine.
func (a *TraceAgent) Snapshot() []*PipelineTrace {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*PipelineTrace, 0, len(a.runs))
	for _, inf := range a.runs {
		cp := *inf.trace
		out = append(out, &cp)
	}
	return out
}
