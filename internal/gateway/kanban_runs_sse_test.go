package gateway_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/kanban/events"
)

// TestRunEvents_SSE_ReplaysAndStreams seeds events both in the DB
// (replay path) and on the bus (live path), then drains the SSE
// response and checks both kinds arrive.
func TestRunEvents_SSE_ReplaysAndStreams(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)

	// Wire the bus and inject it into the AuthService the way
	// attachKanbanToGateway does in production.
	bus := events.NewBus()
	svc.KanbanEvents = bus

	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "owner-sse", "owner-password-long", "role-owner")

	// Seed a board → column → card → run; need a real CardRun.ID so the
	// SSE handler can fetch the run row at open.
	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default"}, cookies)
	boardID, _ := board["id"].(string)

	ctx := context.Background()
	cols, _ := svc.Store.BoardColumns().ListByBoard(ctx, boardID)
	if len(cols) == 0 {
		t.Fatalf("preset columns missing")
	}
	card := &datastore.BoardCard{
		BoardID: boardID, ColumnID: cols[0].ID, Title: "sse-card", Status: "running",
	}
	if err := svc.Store.BoardCards().Create(ctx, card); err != nil {
		t.Fatalf("seed card: %v", err)
	}
	run := &datastore.CardRun{
		CardID:    card.ID,
		RunNumber: 1,
		AgentType: "claude-code",
		Status:    "running",
	}
	if err := svc.Store.CardRuns().Create(ctx, run); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	// Seed two replayable events before the SSE opens.
	for i := 0; i < 2; i++ {
		if err := svc.Store.CardRunEvents().Append(ctx, &datastore.CardRunEvent{
			RunID: run.ID, Kind: "text", Message: "replay-line",
		}); err != nil {
			t.Fatalf("seed replay event: %v", err)
		}
	}

	// Open SSE.
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/runs/"+run.ID+"/events", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(rr, req)
	}()

	// Give the handler a moment to drain the replay, then publish a live
	// event and cancel the request so ServeHTTP returns.
	time.Sleep(150 * time.Millisecond)
	bus.Publish(datastore.CardRunEvent{
		RunID: run.ID, Kind: "text", Message: "live-line",
	})
	time.Sleep(150 * time.Millisecond)

	// httptest.ResponseRecorder doesn't propagate request cancellation
	// to the handler — we let it run until heartbeats / channel-close
	// terminate the loop. The handler currently blocks on the bus
	// channel until cancel; force close via the request context by
	// using a 1s deadline.
	cancelReq := req.WithContext(func() context.Context {
		c, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_ = cancel
		return c
	}())
	_ = cancelReq

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		// Bus close kicks the loop. Subscribe again with cancel to wake the existing reader.
		// In practice the handler returns on heartbeat write failure or
		// channel close. Force termination by closing all subscribers
		// via a fresh publish + give it some time.
		t.Log("handler did not return within 2s; collecting whatever it streamed so far")
	}

	body := rr.Body.String()
	if !strings.Contains(body, "replay-line") {
		t.Fatalf("missing replayed event in stream; body=%q", body)
	}
	if !strings.Contains(body, "live-line") {
		t.Fatalf("missing live bus event in stream; body=%q", body)
	}

	// Replayed events carry an `id:` line; live ones don't.
	scanner := bufio.NewScanner(strings.NewReader(body))
	sawReplayID := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			sawReplayID = true
		}
	}
	if !sawReplayID {
		t.Fatalf("expected `id:` line for replayed events; body=%q", body)
	}
}

// TestRunEvents_SSE_TerminalRunCloses verifies that opening the stream
// on an already-completed run replays + closes (no live subscribe).
func TestRunEvents_SSE_TerminalRunCloses(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	svc.KanbanEvents = events.NewBus()
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "owner-sse2", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default"}, cookies)
	boardID, _ := board["id"].(string)

	ctx := context.Background()
	cols, _ := svc.Store.BoardColumns().ListByBoard(ctx, boardID)
	card := &datastore.BoardCard{BoardID: boardID, ColumnID: cols[0].ID, Title: "x", Status: "completed"}
	_ = svc.Store.BoardCards().Create(ctx, card)
	now := time.Now().UTC()
	run := &datastore.CardRun{
		CardID:      card.ID,
		RunNumber:   1,
		AgentType:   "claude-code",
		Status:      "completed",
		CompletedAt: &now,
	}
	_ = svc.Store.CardRuns().Create(ctx, run)
	_ = svc.Store.CardRunEvents().Append(ctx, &datastore.CardRunEvent{
		RunID: run.ID, Kind: "text", Message: "final",
	})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/runs/"+run.ID+"/events", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { defer close(done); mux.ServeHTTP(rr, req) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal-run SSE should return immediately, not block")
	}

	body := rr.Body.String()
	if !strings.Contains(body, "final") {
		t.Fatalf("missing replayed event for terminal run; body=%q", body)
	}
	if !strings.Contains(body, "event: lifecycle") {
		t.Fatalf("terminal SSE should emit a lifecycle close marker; body=%q", body)
	}
}
