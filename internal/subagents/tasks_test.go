package subagents

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskManager_RunAsync_Completion(t *testing.T) {
	t.Parallel()
	exec := func(ctx context.Context, tid, subID, prompt string) (string, int, error) {
		return "done:" + prompt, 3, nil
	}
	m := NewTaskManager(exec)
	m.MaxConcurrent = 4

	id, err := m.RunAsync("agent-1", "speciall", "hello", 0)
	if err != nil {
		t.Fatalf("RunAsync: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results, err := m.Wait(waitCtx, []string{id}, 2*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results)=%d want 1", len(results))
	}
	got := results[0]
	if got.Status != StatusCompleted {
		t.Fatalf("status=%q want completed", got.Status)
	}
	if got.Result != "done:hello" {
		t.Fatalf("result=%q", got.Result)
	}
	if got.Iterations != 3 {
		t.Fatalf("iterations=%d want 3", got.Iterations)
	}
	if got.Elapsed() <= 0 {
		t.Fatalf("elapsed=%s want >0", got.Elapsed())
	}
}

func TestTaskManager_Failure_IsCaptured(t *testing.T) {
	t.Parallel()
	exec := func(ctx context.Context, tid, subID, prompt string) (string, int, error) {
		return "partial", 2, errors.New("boom")
	}
	m := NewTaskManager(exec)
	id, err := m.RunAsync("a", "n", "q", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Wait(context.Background(), []string{id}, 2*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	got, ok := m.Get(id)
	if !ok {
		t.Fatal("Get returned false")
	}
	if got.Status != StatusFailed {
		t.Fatalf("status=%q want failed", got.Status)
	}
	if got.Error != "boom" {
		t.Fatalf("error=%q", got.Error)
	}
	if got.Result != "partial" {
		t.Fatalf("partial result not preserved: %q", got.Result)
	}
}

func TestTaskManager_Cancel(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	exec := func(ctx context.Context, tid, subID, prompt string) (string, int, error) {
		close(started)
		<-ctx.Done()
		return "", 0, ctx.Err()
	}
	m := NewTaskManager(exec)
	id, err := m.RunAsync("a", "n", "q", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task never started")
	}
	if !m.Cancel(id) {
		t.Fatal("Cancel returned false for known id")
	}
	_, err = m.Wait(context.Background(), []string{id}, 2*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	got, _ := m.Get(id)
	if got.Status != StatusCancelled {
		t.Fatalf("status=%q want cancelled", got.Status)
	}
}

func TestTaskManager_ConcurrencyCap(t *testing.T) {
	t.Parallel()
	var concurrent int64
	var maxSeen int64
	release := make(chan struct{})
	exec := func(ctx context.Context, tid, subID, prompt string) (string, int, error) {
		n := atomic.AddInt64(&concurrent, 1)
		defer atomic.AddInt64(&concurrent, -1)
		for {
			cur := atomic.LoadInt64(&maxSeen)
			if n <= cur || atomic.CompareAndSwapInt64(&maxSeen, cur, n) {
				break
			}
		}
		<-release
		return "ok", 1, nil
	}
	m := NewTaskManager(exec)
	m.MaxConcurrent = 2

	ids := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		id, err := m.RunAsync("a", "n", fmt.Sprintf("t%d", i), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	// Give scheduler a moment to fill the two slots.
	time.Sleep(150 * time.Millisecond)
	if got := atomic.LoadInt64(&concurrent); got > 2 {
		t.Fatalf("concurrency cap breached: running=%d", got)
	}
	close(release)
	if _, err := m.Wait(context.Background(), ids, 5*time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if max := atomic.LoadInt64(&maxSeen); max > 2 {
		t.Fatalf("max concurrent=%d want <=2", max)
	}
}

func TestTaskManager_Wait_UnknownID(t *testing.T) {
	t.Parallel()
	m := NewTaskManager(func(context.Context, string, string, string) (string, int, error) { return "", 0, nil })
	_, err := m.Wait(context.Background(), []string{"nope"}, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error for unknown id")
	}
}

func TestTaskManager_RetainLimit_Evicts(t *testing.T) {
	t.Parallel()
	exec := func(ctx context.Context, tid, subID, prompt string) (string, int, error) {
		return prompt, 1, nil
	}
	m := NewTaskManager(exec)
	m.RetainLimit = 3

	var ids []string
	for i := 0; i < 6; i++ {
		id, err := m.RunAsync("a", "n", fmt.Sprintf("t%d", i), 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		// Drain so tasks become terminal between submissions.
		if _, err := m.Wait(context.Background(), []string{id}, 2*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	got := m.List()
	if len(got) > 3 {
		t.Fatalf("retained=%d want <=3", len(got))
	}
}
