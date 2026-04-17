package subagents

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReport_AppendsEvents(t *testing.T) {
	t.Parallel()
	m := NewTaskManager(func(ctx context.Context, tid, sid, p string) (string, int, error) {
		// Sleep just long enough for the test to report while running.
		select {
		case <-ctx.Done():
		case <-time.After(300 * time.Millisecond):
		}
		return "", 1, nil
	})
	id, err := m.RunAsync("a", "worker", "crunch", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the task to flip to running before reporting (Report
	// rejects reports on pending-but-not-yet-started tasks? No — pending
	// is not terminal, so allowed).
	if err := m.Report(id, "setup", "reading config", KindProgress); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if err := m.Report(id, "", "", KindProgress); err == nil {
		t.Fatal("empty message should error")
	}
	if err := m.Report("nope", "", "msg", KindProgress); err == nil {
		t.Fatal("unknown task should error")
	}

	snap, _ := m.Get(id)
	// Expect: dispatched (lifecycle) + started (lifecycle) + one report.
	// We can't guarantee "started" has fired yet under load, so just
	// check that our report is present.
	found := false
	for _, e := range snap.Events {
		if e.Kind == KindProgress && e.Message == "reading config" && e.Phase == "setup" {
			found = true
		}
	}
	if !found {
		t.Fatalf("progress event not recorded: %+v", snap.Events)
	}

	_, _ = m.Wait(context.Background(), []string{id}, 2*time.Second)
	// After terminal, reports should be rejected.
	if err := m.Report(id, "", "late", KindProgress); err == nil {
		t.Fatal("report on terminal task should error")
	}
}

func TestIntervene_DrainedByChild(t *testing.T) {
	t.Parallel()
	// The exec fn simulates the child draining interventions on each
	// pseudo-iteration.
	drainOnce := make(chan struct{})
	seen := make(chan []Intervention, 1)
	m := NewTaskManager(func(ctx context.Context, tid, sid, p string) (string, int, error) {
		<-drainOnce
		seen <- drainInterventionsFromManagerForTest(tid)
		return "ok", 1, nil
	})
	// Expose a hook so the exec closure can call the manager.
	setTestManager(m)
	defer setTestManager(nil)

	id, err := m.RunAsync("a", "worker", "do stuff", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Queue an intervention before the child drains.
	if err := m.Intervene(id, "master", "refocus on file X"); err != nil {
		t.Fatalf("Intervene: %v", err)
	}
	close(drainOnce)

	drained := <-seen
	if len(drained) != 1 || drained[0].Message != "refocus on file X" {
		t.Fatalf("unexpected drained: %+v", drained)
	}
	// Second drain should be empty.
	if later := m.DrainInterventions(id); len(later) != 0 {
		t.Fatalf("second drain non-empty: %+v", later)
	}
	_, _ = m.Wait(context.Background(), []string{id}, 2*time.Second)

	snap, _ := m.Get(id)
	if len(snap.AppliedInterventions) != 1 {
		t.Fatalf("applied not moved: %+v", snap.AppliedInterventions)
	}
}

func TestIntervene_OnTerminalFails(t *testing.T) {
	t.Parallel()
	m := NewTaskManager(func(ctx context.Context, tid, sid, p string) (string, int, error) {
		return "done", 1, nil
	})
	id, _ := m.RunAsync("a", "worker", "p", 2*time.Second)
	_, _ = m.Wait(context.Background(), []string{id}, 2*time.Second)
	if err := m.Intervene(id, "master", "too late"); err == nil {
		t.Fatal("expected error intervening on terminal task")
	}
}

func TestShareContext_RecordsNote(t *testing.T) {
	t.Parallel()
	m := NewTaskManager(func(ctx context.Context, tid, sid, p string) (string, int, error) {
		<-ctx.Done()
		return "", 0, ctx.Err()
	})
	id, _ := m.RunAsync("a", "worker", "p", 10*time.Second)
	if err := m.ShareContext(id, "master", "PR #42 blocked the deploy on Sonar"); err != nil {
		t.Fatalf("ShareContext: %v", err)
	}
	snap, _ := m.Get(id)
	if len(snap.SharedNotes) != 1 || !strings.Contains(snap.SharedNotes[0].Message, "Sonar") {
		t.Fatalf("shared note: %+v", snap.SharedNotes)
	}
	m.Cancel(id)
}

func TestDashboard_Empty_AndPopulated(t *testing.T) {
	t.Parallel()
	m := NewTaskManager(func(ctx context.Context, tid, sid, p string) (string, int, error) {
		<-ctx.Done()
		return "", 0, ctx.Err()
	})
	if s := m.Dashboard(); s != "" {
		t.Fatalf("empty dashboard: %q", s)
	}
	id, _ := m.RunAsync("a", "reviewer", "review PR #1", 10*time.Second)
	_ = m.Report(id, "analyze", "diff looks safe so far", KindProgress)
	s := m.Dashboard()
	if !strings.Contains(s, id) || !strings.Contains(s, "reviewer") || !strings.Contains(s, "review PR #1") {
		t.Fatalf("dashboard missing fields: %s", s)
	}
	m.Cancel(id)
}

func TestEvents_SinceIndex(t *testing.T) {
	t.Parallel()
	m := NewTaskManager(func(ctx context.Context, tid, sid, p string) (string, int, error) {
		<-ctx.Done()
		return "", 0, ctx.Err()
	})
	id, _ := m.RunAsync("a", "w", "p", 10*time.Second)
	_ = m.Report(id, "", "one", KindProgress)
	_ = m.Report(id, "", "two", KindProgress)
	all := m.Events(id, 0)
	if len(all) < 2 {
		t.Fatalf("expected >=2 events, got %d", len(all))
	}
	tail := m.Events(id, len(all)-1)
	if len(tail) != 1 {
		t.Fatalf("sinceIndex tail: got %d want 1", len(tail))
	}
	m.Cancel(id)
}

// ── test-only mutable hook for the intervene test ─────────────────────────

var (
	tmHookMu sync.Mutex
	tmHook   *TaskManager
)

func setTestManager(m *TaskManager) {
	tmHookMu.Lock()
	defer tmHookMu.Unlock()
	tmHook = m
}

func drainInterventionsFromManagerForTest(tid string) []Intervention {
	tmHookMu.Lock()
	defer tmHookMu.Unlock()
	if tmHook == nil {
		return nil
	}
	return tmHook.DrainInterventions(tid)
}
