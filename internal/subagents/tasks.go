package subagents

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status reports where a background Task currently is in its lifecycle.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// ProgressKind classifies a ProgressEvent so the master agent (and any UI
// listening on the dashboard) can filter quickly.
type ProgressKind string

const (
	// KindProgress is a generic "here's what I'm doing now" update from
	// a child sub-agent. Use for milestones and status pulses.
	KindProgress ProgressKind = "progress"
	// KindBlocked signals the child needs supervision or input from the
	// master before it can proceed.
	KindBlocked ProgressKind = "blocked"
	// KindError records a non-fatal error the child recovered from (or
	// escalated). Fatal errors turn the task Failed directly; this kind
	// is for problems the master should be aware of.
	KindError ProgressKind = "error"
	// KindLifecycle is emitted by the TaskManager itself (task started /
	// cancelled / completed). Children should not emit this kind.
	KindLifecycle ProgressKind = "lifecycle"
)

// ProgressEvent is a timestamped note emitted by a sub-agent (or the
// manager) during a task's lifetime. Events drive both the dashboard
// injected into the master's system prompt and the explicit stream-drain
// tool used for deeper inspection.
type ProgressEvent struct {
	At      time.Time    `json:"at"`
	Kind    ProgressKind `json:"kind"`
	Phase   string       `json:"phase,omitempty"`
	Message string       `json:"message"`
}

// Intervention is a guidance note queued by the master for a running
// sub-agent. It is drained by the child's runner on its next iteration
// and injected as a system message so the child treats it as authoritative.
type Intervention struct {
	At      time.Time `json:"at"`
	From    string    `json:"from"`
	Message string    `json:"message"`
}

// SharedNote is a piece of context the master explicitly pushed into a
// child's session (or a child is sharing with the master / siblings).
// Stored on the Task so ReadContext can replay it; consumers also append
// it to the child's message history when it's a master→child share.
type SharedNote struct {
	At      time.Time `json:"at"`
	From    string    `json:"from"`
	Message string    `json:"message"`
}

// Task is one async sub-agent invocation tracked by the TaskManager.
//
// Task snapshots are copied before being returned so external code cannot
// mutate the manager's internal state. Slices on copies are independent.
type Task struct {
	ID          string
	SubAgentID  string
	SubAgentNm  string
	Task        string
	Status      Status
	StartedAt   time.Time
	CompletedAt time.Time
	Result      string
	Error       string
	Iterations  int

	// Events is the ordered history of ProgressEvents for this task.
	// Populated by children via Report() and by the manager itself for
	// lifecycle transitions.
	Events []ProgressEvent
	// PendingInterventions are guidance notes the master queued that the
	// child hasn't yet drained. Drained entries are moved to AppliedInterventions.
	PendingInterventions []Intervention
	// AppliedInterventions are the interventions the child has already
	// consumed (shown on the dashboard for audit).
	AppliedInterventions []Intervention
	// SharedNotes contains explicit context-share messages to/from this task.
	SharedNotes []SharedNote

	cancel context.CancelFunc
}

// Elapsed returns how long the task has run (or ran, if terminal).
func (t Task) Elapsed() time.Duration {
	start := t.StartedAt
	if start.IsZero() {
		return 0
	}
	if !t.CompletedAt.IsZero() {
		return t.CompletedAt.Sub(start)
	}
	return time.Since(start)
}

// Terminal returns true if the task has reached a final status.
func (t Task) Terminal() bool {
	switch t.Status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// LastEvent returns the most recent ProgressEvent, or a zero event if none.
func (t Task) LastEvent() ProgressEvent {
	if len(t.Events) == 0 {
		return ProgressEvent{}
	}
	return t.Events[len(t.Events)-1]
}

// ExecFn runs a sub-agent task synchronously. Implementations should honour
// ctx cancellation. The first return is the sub-agent's final text
// response; the second is the number of runner iterations consumed.
//
// The taskID is threaded through so the executor can wire child-side tools
// (progress reporting, intervention draining) that are scoped to this task.
// The subAgentID identifies WHICH sub-agent (named specialist) to run;
// the taskID identifies THIS invocation of that sub-agent.
type ExecFn func(ctx context.Context, taskID, subAgentID, taskPrompt string) (response string, iterations int, err error)

// TaskManager schedules background sub-agent runs with bounded concurrency,
// tracks per-task progress events + pending interventions, and produces a
// compact dashboard string for the master's system prompt.
//
// It is safe for concurrent use. Tasks exceeding RetainLimit are evicted
// oldest-first once they reach a terminal status.
type TaskManager struct {
	mu      sync.Mutex
	cond    *sync.Cond
	tasks   map[string]*Task
	running int

	// MaxConcurrent caps how many async tasks run at once. Requests beyond
	// the cap block in RunAsync until a slot frees. Values <= 0 default to 8.
	MaxConcurrent int
	// RetainLimit bounds how many completed tasks the manager keeps in memory.
	// Oldest-first eviction; running tasks are never evicted. Values <= 0
	// keep the last 256 tasks.
	RetainLimit int
	// DefaultTimeout applies when RunAsync is called without an explicit
	// per-task deadline. Values <= 0 default to 30 minutes.
	DefaultTimeout time.Duration
	// MaxEventsPerTask bounds the per-task event ring. Old events are
	// dropped once exceeded. Values <= 0 default to 128.
	MaxEventsPerTask int

	exec ExecFn
}

// NewTaskManager returns a manager that uses exec to run each task. exec is
// called on its own goroutine per invocation and must honour ctx cancellation.
func NewTaskManager(exec ExecFn) *TaskManager {
	m := &TaskManager{
		tasks:            make(map[string]*Task),
		exec:             exec,
		MaxConcurrent:    8,
		RetainLimit:      256,
		DefaultTimeout:   30 * time.Minute,
		MaxEventsPerTask: 128,
	}
	m.cond = sync.NewCond(&m.mu)
	return m
}

// Exec returns the underlying task executor. Exposed so callers that need
// to run a sub-agent synchronously (e.g. the legacy blocking tool) can
// reuse the same code path without rebuilding the runner.
func (m *TaskManager) Exec() ExecFn { return m.exec }

// RunAsync schedules a new task and returns its id immediately. The actual
// run happens on a goroutine; callers poll via Get or wait via Wait.
//
// If the manager is at MaxConcurrent, the goroutine blocks on the internal
// slot cond until a slot frees. timeout overrides DefaultTimeout when > 0.
func (m *TaskManager) RunAsync(subAgentID, subAgentName, taskPrompt string, timeout time.Duration) (string, error) {
	if m.exec == nil {
		return "", errors.New("subagents.TaskManager: no executor configured")
	}
	if taskPrompt == "" {
		return "", errors.New("subagents.TaskManager: task prompt is required")
	}
	if timeout <= 0 {
		timeout = m.DefaultTimeout
	}

	id := uuid.New().String()[:12]
	t := &Task{
		ID:         id,
		SubAgentID: subAgentID,
		SubAgentNm: subAgentName,
		Task:       taskPrompt,
		Status:     StatusPending,
	}

	m.mu.Lock()
	m.tasks[id] = t
	m.appendEventLocked(t, ProgressEvent{
		At:      time.Now(),
		Kind:    KindLifecycle,
		Message: fmt.Sprintf("dispatched to sub-agent %q", subAgentName),
	})
	m.evictLocked()
	m.mu.Unlock()

	go m.run(t, timeout)
	return id, nil
}

func (m *TaskManager) run(t *Task, timeout time.Duration) {
	m.mu.Lock()
	for m.running >= m.maxConcurrent() {
		m.cond.Wait()
	}
	if t.Status == StatusCancelled {
		m.mu.Unlock()
		return
	}
	m.running++
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.cancel = cancel
	t.Status = StatusRunning
	t.StartedAt = time.Now()
	m.appendEventLocked(t, ProgressEvent{
		At:      t.StartedAt,
		Kind:    KindLifecycle,
		Message: "task started",
	})
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.running--
		t.CompletedAt = time.Now()
		t.cancel = nil
		m.cond.Signal()
		m.appendEventLocked(t, ProgressEvent{
			At:      t.CompletedAt,
			Kind:    KindLifecycle,
			Message: "task " + string(t.Status),
		})
		m.mu.Unlock()
		cancel()
	}()

	resp, iters, err := m.exec(ctx, t.ID, t.SubAgentID, t.Task)
	m.mu.Lock()
	t.Iterations = iters
	switch {
	case ctx.Err() == context.Canceled && t.Status == StatusCancelled:
		if err != nil {
			t.Error = err.Error()
		}
	case err != nil:
		t.Status = StatusFailed
		t.Error = err.Error()
		t.Result = resp
	default:
		t.Status = StatusCompleted
		t.Result = resp
	}
	m.mu.Unlock()
}

// Get returns a snapshot of the task with id, or false if unknown.
func (m *TaskManager) Get(id string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return Task{}, false
	}
	return copyTask(t), true
}

// Cancel requests cancellation of a pending or running task. No-op on
// terminal tasks. Returns false if the id is unknown.
func (m *TaskManager) Cancel(id string) bool {
	m.mu.Lock()
	t, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return false
	}
	if t.Terminal() {
		m.mu.Unlock()
		return true
	}
	t.Status = StatusCancelled
	m.appendEventLocked(t, ProgressEvent{
		At:      time.Now(),
		Kind:    KindLifecycle,
		Message: "task cancelled by master",
	})
	c := t.cancel
	m.mu.Unlock()
	if c != nil {
		c()
	}
	return true
}

// List returns a snapshot of all tracked tasks, newest started first.
// Pending tasks appear last (they haven't started yet).
func (m *TaskManager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, copyTask(t))
	}
	sort.Slice(out, func(i, j int) bool {
		si := out[i].StartedAt
		sj := out[j].StartedAt
		if si.IsZero() && sj.IsZero() {
			return out[i].ID < out[j].ID
		}
		if si.IsZero() {
			return false
		}
		if sj.IsZero() {
			return true
		}
		return si.After(sj)
	})
	return out
}

// Active returns only tasks that are still pending or running, newest first.
// This is what the dashboard uses — terminal tasks don't need live supervision.
func (m *TaskManager) Active() []Task {
	all := m.List()
	out := make([]Task, 0, len(all))
	for _, t := range all {
		if !t.Terminal() {
			out = append(out, t)
		}
	}
	return out
}

// Wait blocks until all listed ids have reached a terminal state, timeout
// elapses, or ctx is cancelled. It returns snapshots of all requested tasks
// (even unknown ones, marked Status=="" so callers can detect them). Waits
// with timeout <= 0 default to 5 minutes.
func (m *TaskManager) Wait(ctx context.Context, ids []string, timeout time.Duration) ([]Task, error) {
	if len(ids) == 0 {
		return nil, errors.New("subagents.TaskManager: ids is required")
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	poll := 100 * time.Millisecond

	for {
		m.mu.Lock()
		out := make([]Task, 0, len(ids))
		allDone := true
		for _, id := range ids {
			if t, ok := m.tasks[id]; ok {
				out = append(out, copyTask(t))
				if !t.Terminal() {
					allDone = false
				}
			} else {
				out = append(out, Task{ID: id})
				allDone = false
			}
		}
		m.mu.Unlock()
		if allDone {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(poll):
		}
		if time.Now().After(deadline) {
			return out, fmt.Errorf("subagents.TaskManager: wait timed out after %s", timeout)
		}
		if poll < 2*time.Second {
			poll *= 2
		}
	}
}

// Report appends a ProgressEvent to a task's event log. Called by the
// child sub-agent via the `supervisor_report` tool to signal milestones,
// blocks, or recoverable errors. Kind is normalised; an unknown kind is
// stored as KindProgress.
func (m *TaskManager) Report(taskID string, phase, message string, kind ProgressKind) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return errors.New("subagents: report message is required")
	}
	if kind == "" || kind == KindLifecycle {
		kind = KindProgress
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[taskID]
	if !ok {
		return fmt.Errorf("subagents: unknown task %q", taskID)
	}
	if t.Terminal() {
		return fmt.Errorf("subagents: task %q already %s", taskID, t.Status)
	}
	m.appendEventLocked(t, ProgressEvent{
		At:      time.Now(),
		Kind:    kind,
		Phase:   strings.TrimSpace(phase),
		Message: message,
	})
	return nil
}

// Intervene queues a guidance message for a running task. The child drains
// it on its next iteration via DrainInterventions and applies it as a
// system instruction. Interventions on terminal tasks are rejected.
func (m *TaskManager) Intervene(taskID, from, guidance string) error {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return errors.New("subagents: intervention guidance is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[taskID]
	if !ok {
		return fmt.Errorf("subagents: unknown task %q", taskID)
	}
	if t.Terminal() {
		return fmt.Errorf("subagents: task %q already %s — cannot intervene", taskID, t.Status)
	}
	t.PendingInterventions = append(t.PendingInterventions, Intervention{
		At:      time.Now(),
		From:    from,
		Message: guidance,
	})
	m.appendEventLocked(t, ProgressEvent{
		At:      time.Now(),
		Kind:    KindProgress,
		Phase:   "supervision",
		Message: "master queued intervention: " + truncate(guidance, 200),
	})
	return nil
}

// DrainInterventions atomically moves all pending interventions into
// "applied" and returns the drained list. The child sub-agent's runner
// calls this at the top of each iteration and injects the returned list
// into its system prompt.
//
// Terminal tasks silently return nil — cancellation already happened.
func (m *TaskManager) DrainInterventions(taskID string) []Intervention {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[taskID]
	if !ok {
		return nil
	}
	if len(t.PendingInterventions) == 0 {
		return nil
	}
	drained := t.PendingInterventions
	t.PendingInterventions = nil
	t.AppliedInterventions = append(t.AppliedInterventions, drained...)
	return drained
}

// ShareContext records an explicit context-share note. Used when the
// master wants a running child to see a piece of context that wasn't part
// of the original task prompt (or vice-versa). Storage here is the audit
// trail; the caller is still responsible for appending to the child's
// memory session if they want the note to influence the LLM.
func (m *TaskManager) ShareContext(taskID, from, note string) error {
	note = strings.TrimSpace(note)
	if note == "" {
		return errors.New("subagents: share note is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[taskID]
	if !ok {
		return fmt.Errorf("subagents: unknown task %q", taskID)
	}
	t.SharedNotes = append(t.SharedNotes, SharedNote{
		At:      time.Now(),
		From:    from,
		Message: note,
	})
	m.appendEventLocked(t, ProgressEvent{
		At:      time.Now(),
		Kind:    KindProgress,
		Phase:   "share",
		Message: fmt.Sprintf("%s shared context: %s", from, truncate(note, 200)),
	})
	return nil
}

// Events returns a copy of the event log for a task, or nil if unknown.
// Optional sinceIndex skips the first N events (for efficient streaming).
func (m *TaskManager) Events(taskID string, sinceIndex int) []ProgressEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[taskID]
	if !ok {
		return nil
	}
	if sinceIndex < 0 {
		sinceIndex = 0
	}
	if sinceIndex >= len(t.Events) {
		return nil
	}
	out := make([]ProgressEvent, len(t.Events)-sinceIndex)
	copy(out, t.Events[sinceIndex:])
	return out
}

// Dashboard returns a compact human-readable summary of all currently
// active tasks (pending + running). Injected into the master agent's
// system prompt on every iteration so oversight is ambient, not polled.
//
// Empty string means no active children; callers should omit the
// dashboard block from the prompt in that case.
func (m *TaskManager) Dashboard() string {
	active := m.Active()
	if len(active) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("You currently have %d active sub-agent task(s). Supervise them autonomously — queue guidance via subagent_intervene, inspect details via subagent_stream, cancel via subagent_cancel.\n\n", len(active)))
	for i, t := range active {
		b.WriteString(fmt.Sprintf("  %d. task=%s  agent=%q  status=%s  elapsed=%s\n",
			i+1, t.ID, t.SubAgentNm, t.Status, t.Elapsed().Round(time.Second)))
		if goal := truncate(t.Task, 140); goal != "" {
			b.WriteString("     goal: " + goal + "\n")
		}
		if last := t.LastEvent(); last.Message != "" {
			phase := ""
			if last.Phase != "" {
				phase = " [" + last.Phase + "]"
			}
			b.WriteString(fmt.Sprintf("     last%s (%s): %s\n", phase, last.Kind, truncate(last.Message, 200)))
		}
		if n := len(t.PendingInterventions); n > 0 {
			b.WriteString(fmt.Sprintf("     pending interventions: %d (child will drain next iteration)\n", n))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ── internals ────────────────────────────────────────────────────────────────

func (m *TaskManager) maxConcurrent() int {
	if m.MaxConcurrent <= 0 {
		return 8
	}
	return m.MaxConcurrent
}

func (m *TaskManager) retainLimit() int {
	if m.RetainLimit <= 0 {
		return 256
	}
	return m.RetainLimit
}

func (m *TaskManager) maxEvents() int {
	if m.MaxEventsPerTask <= 0 {
		return 128
	}
	return m.MaxEventsPerTask
}

func (m *TaskManager) appendEventLocked(t *Task, ev ProgressEvent) {
	t.Events = append(t.Events, ev)
	if max := m.maxEvents(); len(t.Events) > max {
		// Drop oldest to keep bound.
		t.Events = append([]ProgressEvent(nil), t.Events[len(t.Events)-max:]...)
	}
}

func (m *TaskManager) evictLocked() {
	limit := m.retainLimit()
	if len(m.tasks) <= limit {
		return
	}
	type entry struct {
		id string
		at time.Time
	}
	var terminals []entry
	for id, t := range m.tasks {
		if t.Terminal() {
			terminals = append(terminals, entry{id, t.CompletedAt})
		}
	}
	sort.Slice(terminals, func(i, j int) bool {
		return terminals[i].at.Before(terminals[j].at)
	})
	drop := len(m.tasks) - limit
	for i := 0; i < drop && i < len(terminals); i++ {
		delete(m.tasks, terminals[i].id)
	}
}

func copyTask(t *Task) Task {
	out := *t
	if len(t.Events) > 0 {
		out.Events = append([]ProgressEvent(nil), t.Events...)
	}
	if len(t.PendingInterventions) > 0 {
		out.PendingInterventions = append([]Intervention(nil), t.PendingInterventions...)
	}
	if len(t.AppliedInterventions) > 0 {
		out.AppliedInterventions = append([]Intervention(nil), t.AppliedInterventions...)
	}
	if len(t.SharedNotes) > 0 {
		out.SharedNotes = append([]SharedNote(nil), t.SharedNotes...)
	}
	out.cancel = nil
	return out
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
