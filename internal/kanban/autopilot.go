package kanban

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// Autopilot turns a one-shot Dispatch into a loop. Two modes match
// kanbots.dev:
//
//   - "feature-dev": round-robin a set of personas (product / engineer /
//     reviewer / tester) across N parallel slots on the same card. Keeps
//     going until the card is done, the operator hits stop, or the
//     session cost budget is exceeded.
//
//   - "qa":          run a configurable suite of check commands
//     (typecheck / tests / lint / build / e2e) inside the card's worktree,
//     and dispatch a "fix" run against each failing check.
//
// Both modes share the same Run() entry point; the Mode field controls
// which path is taken. Sessions are tracked in-memory only for the
// duration of the process; if the daemon restarts mid-loop the user can
// re-dispatch — runs already recorded in `card_runs` are preserved.
type Autopilot struct {
	Svc *DispatchService

	mu       sync.Mutex
	sessions map[string]*AutopilotSession
}

// NewAutopilot returns an Autopilot bound to a dispatch service.
func NewAutopilot(svc *DispatchService) *Autopilot {
	return &Autopilot{
		Svc:      svc,
		sessions: make(map[string]*AutopilotSession),
	}
}

// AutopilotSession is the live state of a running autopilot loop. Exposed
// so callers (HTTP API, CLI) can list / stop sessions.
type AutopilotSession struct {
	ID         string    `json:"id"`
	CardID     string    `json:"card_id"`
	Mode       string    `json:"mode"`             // "feature-dev" | "qa"
	StartedAt  time.Time `json:"started_at"`
	StoppedAt  *time.Time `json:"stopped_at,omitempty"`
	Status     string    `json:"status"`           // "running" | "stopped" | "completed" | "failed"
	Cycles     int       `json:"cycles"`
	ChildRuns  []string  `json:"child_runs"`
	TotalCost  float64   `json:"total_cost_usd"`
	LastError  string    `json:"last_error,omitempty"`

	cancel context.CancelFunc
}

// FeatureDevOpts configures the feature-dev autopilot mode.
type FeatureDevOpts struct {
	// PersonaIDs cycled through in round-robin order. Empty means "use the
	// card's default agent without a persona lens" (single-thread mode).
	PersonaIDs []string
	// Parallelism is the number of concurrent dispatches. Clamped to
	// [1, 4] per kanbots.dev's documented upper bound.
	Parallelism int
	// BudgetUSD is the session-level cost cap. 0 = no cap.
	BudgetUSD float64
	// MaxCycles is the most rounds of (parallelism × personas) the
	// session will run before stopping itself. 0 = unbounded.
	MaxCycles int
}

// QAOpts configures the qa autopilot mode.
type QAOpts struct {
	// Checks is the ordered list of shell commands to run inside the
	// worktree. Standard names: "typecheck", "tests", "lint", "build", "e2e".
	// The command string is run via `sh -c`.
	Checks []QACheck
	// FixAgentID is the agent to dispatch fix runs against. Falls back
	// to the card's assignee when empty.
	FixAgentID string
	// MaxFixAttempts is the most consecutive failing runs allowed per
	// check before giving up. Default 3.
	MaxFixAttempts int
	// BudgetUSD caps total session spend across all fix runs.
	BudgetUSD float64
}

// QACheck is one entry in the QA suite.
type QACheck struct {
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
}

// StartFeatureDev kicks off a feature-dev autopilot session and returns
// immediately; the loop runs in the background.
func (a *Autopilot) StartFeatureDev(ctx context.Context, cardID string, opts FeatureDevOpts) (*AutopilotSession, error) {
	card, err := a.Svc.Store.BoardCards().Get(ctx, cardID)
	if err != nil {
		return nil, fmt.Errorf("autopilot: get card: %w", err)
	}
	if opts.Parallelism < 1 {
		opts.Parallelism = 1
	}
	if opts.Parallelism > 4 {
		opts.Parallelism = 4
	}

	sess := &AutopilotSession{
		ID:        uuid.NewString(),
		CardID:    card.ID,
		Mode:      "feature-dev",
		StartedAt: time.Now().UTC(),
		Status:    "running",
	}
	bgCtx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel

	a.mu.Lock()
	a.sessions[sess.ID] = sess
	a.mu.Unlock()

	go a.runFeatureDevLoop(bgCtx, sess, card, opts)
	return sess, nil
}

// StartQA kicks off a qa autopilot session.
func (a *Autopilot) StartQA(ctx context.Context, cardID string, opts QAOpts) (*AutopilotSession, error) {
	card, err := a.Svc.Store.BoardCards().Get(ctx, cardID)
	if err != nil {
		return nil, fmt.Errorf("autopilot: get card: %w", err)
	}
	if card.WorktreePath == "" {
		return nil, fmt.Errorf("autopilot qa: card %q has no active worktree (dispatch a run first)", card.ID)
	}
	if opts.MaxFixAttempts == 0 {
		opts.MaxFixAttempts = 3
	}

	sess := &AutopilotSession{
		ID:        uuid.NewString(),
		CardID:    card.ID,
		Mode:      "qa",
		StartedAt: time.Now().UTC(),
		Status:    "running",
	}
	bgCtx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel

	a.mu.Lock()
	a.sessions[sess.ID] = sess
	a.mu.Unlock()

	go a.runQALoop(bgCtx, sess, card, opts)
	return sess, nil
}

// Stop terminates a running session. Returns an error if the session id
// is unknown or already stopped.
func (a *Autopilot) Stop(sessionID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	sess, ok := a.sessions[sessionID]
	if !ok {
		return fmt.Errorf("autopilot: unknown session %q", sessionID)
	}
	if sess.Status != "running" {
		return fmt.Errorf("autopilot: session %q already %s", sessionID, sess.Status)
	}
	if sess.cancel != nil {
		sess.cancel()
	}
	sess.Status = "stopped"
	now := time.Now().UTC()
	sess.StoppedAt = &now
	return nil
}

// Get returns the current state of a session, or nil if unknown.
func (a *Autopilot) Get(sessionID string) *AutopilotSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[sessionID]; ok {
		// Copy so callers can't race against our updates.
		cp := *s
		return &cp
	}
	return nil
}

// List returns every session, sorted by start time descending.
func (a *Autopilot) List() []AutopilotSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AutopilotSession, 0, len(a.sessions))
	for _, s := range a.sessions {
		out = append(out, *s)
	}
	return out
}

// ── feature-dev loop ────────────────────────────────────────────────────

func (a *Autopilot) runFeatureDevLoop(ctx context.Context, sess *AutopilotSession, card *datastore.BoardCard, opts FeatureDevOpts) {
	defer a.markStopped(sess)

	personaIdx := 0
	for cycle := 0; ; cycle++ {
		if opts.MaxCycles > 0 && cycle >= opts.MaxCycles {
			a.markStatus(sess, "completed", "")
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Budget gate.
		if opts.BudgetUSD > 0 && sess.TotalCost >= opts.BudgetUSD {
			a.markStatus(sess, "completed", "session budget reached")
			return
		}

		// Dispatch up to Parallelism runs concurrently. Each picks the
		// next persona in round-robin order. We wait for ALL of them to
		// finalize before scheduling the next cycle so the operator can
		// inspect intermediate state on the kanban board.
		var wg sync.WaitGroup
		for slot := 0; slot < opts.Parallelism; slot++ {
			personaID := ""
			if len(opts.PersonaIDs) > 0 {
				personaID = opts.PersonaIDs[personaIdx%len(opts.PersonaIDs)]
				personaIdx++
			}
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				run, err := a.Svc.Dispatch(ctx, card.ID, DispatchOpts{
					PersonaID: p,
					CreatedBy: "autopilot:" + sess.ID,
				})
				if err != nil {
					a.recordError(sess, err.Error())
					return
				}
				a.appendChildRun(sess, run.ID)
				// Wait for the run to finalize so we can attribute cost
				// before the next cycle decides whether to keep going.
				a.waitRunDone(ctx, run.ID)
				if final, err := a.Svc.Store.CardRuns().Get(ctx, run.ID); err == nil {
					a.addCost(sess, final.CostUSD)
				}
			}(personaID)
		}
		wg.Wait()

		// Cycle finished; if the card is now "completed" we stop.
		if fresh, err := a.Svc.Store.BoardCards().Get(ctx, card.ID); err == nil {
			if fresh.Status == "completed" {
				a.markStatus(sess, "completed", "")
				return
			}
		}
		sess.Cycles = cycle + 1
	}
}

// ── qa loop ─────────────────────────────────────────────────────────────

func (a *Autopilot) runQALoop(ctx context.Context, sess *AutopilotSession, card *datastore.BoardCard, opts QAOpts) {
	defer a.markStopped(sess)

	worktree := card.WorktreePath
	for _, check := range opts.Checks {
		attempts := 0
	checkLoop:
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if opts.BudgetUSD > 0 && sess.TotalCost >= opts.BudgetUSD {
				a.markStatus(sess, "completed", "session budget reached")
				return
			}

			out, exitCode := runShellCheck(ctx, worktree, check.Cmd)
			if exitCode == 0 {
				// Pass; move on to next check.
				break checkLoop
			}
			if attempts >= opts.MaxFixAttempts {
				a.recordError(sess, fmt.Sprintf("qa: %q failed after %d fix attempts", check.Name, attempts))
				a.markStatus(sess, "failed", "")
				return
			}
			attempts++

			// Dispatch a fix run with the failure log as the prompt
			// override so the agent sees what to fix.
			fixPrompt := fmt.Sprintf(
				"QA check %q failed in the card's worktree (%s). The check command was:\n\n    %s\n\nOutput / error:\n\n%s\n\nDiagnose and fix. Re-run the same command yourself to verify before reporting done.",
				check.Name, worktree, check.Cmd, truncate(out, 4096),
			)
			run, err := a.Svc.Dispatch(ctx, card.ID, DispatchOpts{
				AgentID:           opts.FixAgentID,
				CreatedBy:         "autopilot-qa:" + sess.ID,
				PromptOverride:    fixPrompt,
				ReuseWorktreePath: worktree,
				ReuseBranch:       card.Branch,
			})
			if err != nil {
				a.recordError(sess, err.Error())
				a.markStatus(sess, "failed", "")
				return
			}
			a.appendChildRun(sess, run.ID)
			a.waitRunDone(ctx, run.ID)
			if final, err := a.Svc.Store.CardRuns().Get(ctx, run.ID); err == nil {
				a.addCost(sess, final.CostUSD)
			}
		}
	}
	a.markStatus(sess, "completed", "")
}

// runShellCheck runs `sh -c <cmd>` in the worktree and returns combined
// output plus the exit code. A non-zero exit code (or any error) is the
// signal we need a fix dispatch.
func runShellCheck(ctx context.Context, dir, cmd string) (string, int) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Dir = dir
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	if err == nil {
		return buf.String(), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return buf.String(), ee.ExitCode()
	}
	return buf.String() + "\n[autopilot] " + err.Error(), 1
}

// waitRunDone blocks until the given run reaches a terminal status. Polls
// the datastore at 500ms; cheap and avoids wiring a pub/sub for the
// short-lived autopilot use case.
func (a *Autopilot) waitRunDone(ctx context.Context, runID string) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run, err := a.Svc.Store.CardRuns().Get(ctx, runID)
			if err != nil {
				return
			}
			switch run.Status {
			case "completed", "completed_paused", "failed", "stopped":
				return
			}
		}
	}
}

func (a *Autopilot) appendChildRun(sess *AutopilotSession, runID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sess.ChildRuns = append(sess.ChildRuns, runID)
}

func (a *Autopilot) addCost(sess *AutopilotSession, usd float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sess.TotalCost += usd
}

func (a *Autopilot) recordError(sess *AutopilotSession, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sess.LastError = msg
}

func (a *Autopilot) markStatus(sess *AutopilotSession, status, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sess.Status = status
	if errMsg != "" {
		sess.LastError = errMsg
	}
	now := time.Now().UTC()
	sess.StoppedAt = &now
}

func (a *Autopilot) markStopped(sess *AutopilotSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if sess.Status == "running" {
		sess.Status = "stopped"
		now := time.Now().UTC()
		sess.StoppedAt = &now
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…[truncated " + strings.Repeat("·", 0) + "]"
}
