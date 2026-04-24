package pipeline

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/devops/github"
	"github.com/opsintelligence/opsintelligence/internal/devops/sandbox"
	"go.uber.org/zap"
)

// ── Priority ──────────────────────────────────────────────────────────────────

// Priority controls queue ordering. Lower value = higher priority.
type Priority int

const (
	PriorityUrgent     Priority = 0 // security/hot-fix PRs, manual escalation
	PriorityNormal     Priority = 1 // default for /pr-review commands
	PriorityBackground Priority = 2 // scheduled / batch reviews
)

// ── PRJob ─────────────────────────────────────────────────────────────────────

// PRJob is a unit of work submitted to the Coordinator.
type PRJob struct {
	Input    StageInput
	Priority Priority
	// Added is set automatically by Coordinator.Submit.
	Added time.Time
}

// ── Priority queue (heap) ─────────────────────────────────────────────────────

type heapItem struct {
	job   PRJob
	index int
}

type priorQueue []*heapItem

func (q priorQueue) Len() int { return len(q) }
func (q priorQueue) Less(i, j int) bool {
	if q[i].job.Priority != q[j].job.Priority {
		return q[i].job.Priority < q[j].job.Priority
	}
	return q[i].job.Added.Before(q[j].job.Added)
}
func (q priorQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}
func (q *priorQueue) Push(x any) {
	item := x.(*heapItem)
	item.index = len(*q)
	*q = append(*q, item)
}
func (q *priorQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return item
}

// ── Dedup cache ───────────────────────────────────────────────────────────────

type dedupKey struct {
	fullRepo string
	number   int
	sha      string
}

// dedupCache records the last run time for each (repo, PR, SHA) tuple.
type dedupCache struct {
	mu      sync.Mutex
	entries map[dedupKey]time.Time
}

func newDedupCache() *dedupCache {
	return &dedupCache{entries: make(map[dedupKey]time.Time)}
}

// seen returns true if this exact job was submitted within window.
func (c *dedupCache) seen(key dedupKey, window time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.entries[key]
	return ok && time.Since(last) < window
}

func (c *dedupCache) record(key dedupKey) {
	c.mu.Lock()
	c.entries[key] = time.Now()
	c.mu.Unlock()
}

// ── Coordinator config ────────────────────────────────────────────────────────

// CoordinatorConfig holds all tuning parameters for the Coordinator.
type CoordinatorConfig struct {
	// MaxWorkers is the maximum number of PR pipelines running concurrently.
	// Each worker runs all five stages for one PR. Default 16.
	MaxWorkers int

	// DedupWindow is the minimum interval between two reviews of the same
	// PR at the same commit SHA. Default 10 minutes.
	DedupWindow time.Duration

	// DefaultOrg is used when PRJob.Input.Owner is empty.
	DefaultOrg string
}

func (c *CoordinatorConfig) applyDefaults() {
	if c.MaxWorkers <= 0 {
		c.MaxWorkers = 16
	}
	if c.DedupWindow <= 0 {
		c.DedupWindow = 10 * time.Minute
	}
}

// ── Run result ────────────────────────────────────────────────────────────────

// RunResult is returned asynchronously via a channel when a pipeline completes.
type RunResult struct {
	RunID      string
	PRNumber   int
	FullRepo   string
	Verdict    string
	InlineCount int
	DurationMs int64
	ModelUsed  string
	IsLocal    bool
	Error      error
}

// ── Coordinator ───────────────────────────────────────────────────────────────

// waiters is a per-run response channel for SubmitAndWait callers.
type waiters struct {
	mu sync.Mutex
	m  map[string]chan RunResult
}

func newWaiters() *waiters { return &waiters{m: make(map[string]chan RunResult)} }

func (w *waiters) register(runID string) chan RunResult {
	ch := make(chan RunResult, 1)
	w.mu.Lock()
	w.m[runID] = ch
	w.mu.Unlock()
	return ch
}

func (w *waiters) notify(r RunResult) {
	w.mu.Lock()
	ch, ok := w.m[r.RunID]
	if ok {
		delete(w.m, r.RunID)
	}
	w.mu.Unlock()
	if ok {
		ch <- r
	}
}

// Coordinator is the enterprise PR review pipeline orchestrator.
//
// It maintains a priority work queue, an adaptive worker pool, request
// deduplication, and wires together all five pipeline stages per PR.
// All LLM calls flow through the LLMRouter which enforces per-provider
// token-bucket rate limits — there is no artificial cap on LLM concurrency
// beyond what the provider's RPM allows.
type Coordinator struct {
	cfg     CoordinatorConfig
	router  *LLMRouter
	agent   *TraceAgent
	ghClient *github.Client
	sandbox *sandbox.Runner
	detector *sandbox.Detector
	log     *zap.Logger

	// Queue state
	queue    priorQueue
	queueMu  sync.Mutex
	queueSig chan struct{} // signals a new item was pushed

	// Worker pool
	workerSem chan struct{} // counting semaphore, cap = MaxWorkers
	active    atomic.Int64

	// Dedup
	dedup *dedupCache

	// Results channel — callers may subscribe by reading Results.
	Results chan RunResult

	// w holds per-run response channels for SubmitAndWait callers.
	w *waiters
}

// NewCoordinator constructs a Coordinator. Call Start to begin processing.
func NewCoordinator(
	cfg CoordinatorConfig,
	router *LLMRouter,
	traceAgent *TraceAgent,
	ghClient *github.Client,
	sbRunner *sandbox.Runner,
	detector *sandbox.Detector,
	log *zap.Logger,
) *Coordinator {
	cfg.applyDefaults()
	heap.Init(&priorQueue{})
	c := &Coordinator{
		cfg:      cfg,
		router:   router,
		agent:    traceAgent,
		ghClient: ghClient,
		sandbox:  sbRunner,
		detector: detector,
		log:      log,
		queue:    priorQueue{},
		queueSig: make(chan struct{}, 1),
		workerSem: make(chan struct{}, cfg.MaxWorkers),
		dedup:   newDedupCache(),
		Results: make(chan RunResult, 256),
		w:       newWaiters(),
	}
	return c
}

// SubmitAndWait submits a job and blocks until the pipeline completes or ctx
// is cancelled. Returns a human-readable summary string (suitable for channel
// replies) and any hard error.
func (c *Coordinator) SubmitAndWait(ctx context.Context, job PRJob) (string, error) {
	if job.Added.IsZero() {
		job.Added = time.Now()
	}
	if job.Input.RunID == "" {
		job.Input.RunID = newRunID()
	}
	ch := c.w.register(job.Input.RunID)

	if _, err := c.Submit(job); err != nil {
		c.w.notify(RunResult{RunID: job.Input.RunID, Error: err})
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		if r.Error != nil {
			return "", r.Error
		}
		return formatRunResult(r), nil
	}
}

func formatRunResult(r RunResult) string {
	model := r.ModelUsed
	if r.IsLocal {
		model = "local-intel/" + model
	}
	return fmt.Sprintf("PR review complete: %s#%d  verdict=%s  inline=%d  model=%s  duration=%dms",
		r.FullRepo, r.PRNumber, r.Verdict, r.InlineCount, model, r.DurationMs)
}

// Start launches the dispatch loop. Call in a goroutine; returns when ctx is done.
func (c *Coordinator) Start(ctx context.Context) {
	if c.log != nil {
		c.log.Info("pipeline coordinator started",
			zap.Int("max_workers", c.cfg.MaxWorkers),
		)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.queueSig:
			c.dispatch(ctx)
		}
	}
}

// Submit enqueues a PR job. Returns the runID and an error if the job is
// deduplicated. Non-blocking: the job is queued and processed asynchronously.
func (c *Coordinator) Submit(job PRJob) (string, error) {
	if job.Added.IsZero() {
		job.Added = time.Now()
	}
	if job.Input.RunID == "" {
		job.Input.RunID = newRunID()
	}
	if job.Input.Owner == "" && c.cfg.DefaultOrg != "" {
		job.Input.Owner = c.cfg.DefaultOrg
	}
	if job.Input.FullRepo == "" {
		job.Input.FullRepo = job.Input.Owner + "/" + job.Input.Repo
	}

	// Dedup check.
	key := dedupKey{
		fullRepo: job.Input.FullRepo,
		number:   job.Input.Number,
		sha:      job.Input.CommitSHA,
	}
	if c.dedup.seen(key, c.cfg.DedupWindow) {
		return "", fmt.Errorf("coordinator: duplicate review skipped for %s#%d (same commit within %s)",
			job.Input.FullRepo, job.Input.Number, c.cfg.DedupWindow)
	}
	c.dedup.record(key)

	c.queueMu.Lock()
	heap.Push(&c.queue, &heapItem{job: job})
	c.queueMu.Unlock()

	// Non-blocking signal.
	select {
	case c.queueSig <- struct{}{}:
	default:
	}

	if c.log != nil {
		c.log.Info("pipeline job queued",
			zap.String("run_id", job.Input.RunID),
			zap.String("repo", job.Input.FullRepo),
			zap.Int("pr", job.Input.Number),
			zap.Int("priority", int(job.Priority)),
		)
	}
	return job.Input.RunID, nil
}

// ActiveCount returns the number of pipelines currently running.
func (c *Coordinator) ActiveCount() int {
	return int(c.active.Load())
}

// QueueDepth returns the number of jobs waiting to start.
func (c *Coordinator) QueueDepth() int {
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	return c.queue.Len()
}

// dispatch pulls as many jobs from the queue as worker slots allow.
func (c *Coordinator) dispatch(ctx context.Context) {
	for {
		c.queueMu.Lock()
		if c.queue.Len() == 0 {
			c.queueMu.Unlock()
			return
		}
		// Try to acquire a worker slot without blocking.
		select {
		case c.workerSem <- struct{}{}:
		default:
			c.queueMu.Unlock()
			return // all slots busy; will retry when a worker finishes
		}
		item := heap.Pop(&c.queue).(*heapItem)
		c.queueMu.Unlock()

		c.active.Add(1)
		go func(job PRJob) {
			defer func() {
				<-c.workerSem
				c.active.Add(-1)
				// Signal again in case more items are queued.
				select {
				case c.queueSig <- struct{}{}:
				default:
				}
			}()
			c.runPipeline(ctx, job)
		}(item.job)
	}
}

// runPipeline executes all five stages for one PR and publishes the result.
func (c *Coordinator) runPipeline(ctx context.Context, job PRJob) {
	in := job.Input
	start := time.Now()

	if c.log != nil {
		c.log.Info("pipeline started",
			zap.String("run_id", in.RunID),
			zap.String("repo", in.FullRepo),
			zap.Int("pr", in.Number),
		)
	}

	// ── Stage 1: Fetch ──────────────────────────────────────────────────────
	fetch, err := StageFetch(ctx, in, c.ghClient, c.agent)
	if err != nil {
		c.publishError(in, start, err)
		return
	}
	// Propagate commit SHA from fetched PR if not already set.
	if in.CommitSHA == "" && fetch.PR != nil {
		in.CommitSHA = fetch.PR.Head.SHA
	}
	if fetch.PR != nil && in.HeadRef == "" {
		in.HeadRef = fetch.PR.Head.Ref
	}

	// ── Stage 2: Analyse ────────────────────────────────────────────────────
	analyse, err := StageAnalyse(ctx, in, fetch, c.detector, c.agent)
	if err != nil {
		// Analysis failure is non-fatal: proceed without CI commands.
		analyse = &AnalyseResult{}
		if c.log != nil {
			c.log.Warn("pipeline analyse stage failed (continuing)",
				zap.String("run_id", in.RunID), zap.Error(err))
		}
	}

	// ── Stage 3: Sandbox ────────────────────────────────────────────────────
	sandboxRes, err := StageSandbox(ctx, in, fetch, analyse, c.sandbox, c.agent)
	if err != nil {
		// Sandbox failure is non-fatal: proceed without sandbox results.
		sandboxRes = &SandboxResult{}
		if c.log != nil {
			c.log.Warn("pipeline sandbox stage failed (continuing)",
				zap.String("run_id", in.RunID), zap.Error(err))
		}
	}

	// ── Stage 4: LLM Review (rate-limited, parallel across PRs) ─────────────
	review, err := StageReview(ctx, in, fetch, sandboxRes, c.router, c.agent)
	if err != nil {
		c.publishError(in, start, fmt.Errorf("review: %w", err))
		return
	}

	// ── Stage 5: Post ────────────────────────────────────────────────────────
	post, err := StagePost(ctx, in, fetch, review, sandboxRes, c.ghClient, c.agent)
	if err != nil {
		c.publishError(in, start, fmt.Errorf("post: %w", err))
		return
	}

	dur := time.Since(start).Milliseconds()
	if c.log != nil {
		c.log.Info("pipeline completed",
			zap.String("run_id", in.RunID),
			zap.String("repo", in.FullRepo),
			zap.Int("pr", in.Number),
			zap.String("verdict", review.Event),
			zap.Int("inline_comments", len(review.Comments)),
			zap.Bool("dry_run", post.DryRun),
			zap.Bool("local_intel", review.IsLocal),
			zap.Int64("duration_ms", dur),
		)
	}

	result := RunResult{
		RunID:       in.RunID,
		PRNumber:    in.Number,
		FullRepo:    in.FullRepo,
		Verdict:     review.Event,
		InlineCount: len(review.Comments),
		DurationMs:  dur,
		ModelUsed:   review.ModelUsed,
		IsLocal:     review.IsLocal,
	}
	c.w.notify(result)
	select {
	case c.Results <- result:
	default:
	}
}

func (c *Coordinator) publishError(in StageInput, start time.Time, err error) {
	if c.log != nil {
		c.log.Error("pipeline failed",
			zap.String("run_id", in.RunID),
			zap.String("repo", in.FullRepo),
			zap.Int("pr", in.Number),
			zap.Error(err),
		)
	}
	result := RunResult{
		RunID:      in.RunID,
		PRNumber:   in.Number,
		FullRepo:   in.FullRepo,
		DurationMs: time.Since(start).Milliseconds(),
		Error:      err,
	}
	c.w.notify(result)
	select {
	case c.Results <- result:
	default:
	}
}
