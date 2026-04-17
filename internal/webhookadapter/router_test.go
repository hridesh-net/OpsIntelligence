package webhookadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubAdapter lets tests dial in exactly the behaviour they need without
// pulling in a real provider.
type stubAdapter struct {
	name, path string
	enabled    bool
	verifyErr  error
	parseErr   error
	filter     FilterResult
	renderErr  error
	rendered   string
	seenBodies [][]byte
}

func (s *stubAdapter) Name() string  { return s.name }
func (s *stubAdapter) Path() string  { return s.path }
func (s *stubAdapter) Enabled() bool { return s.enabled }
func (s *stubAdapter) Verify(_ *http.Request, body []byte) error {
	s.seenBodies = append(s.seenBodies, body)
	return s.verifyErr
}
func (s *stubAdapter) Parse(r *http.Request, body []byte) (Event, error) {
	if s.parseErr != nil {
		return Event{}, s.parseErr
	}
	return Event{
		Kind:       r.Header.Get("X-Kind"),
		Action:     r.Header.Get("X-Action"),
		DeliveryID: r.Header.Get("X-Delivery"),
		Payload:    map[string]interface{}{"body_len": len(body)},
		RawBody:    body,
	}, nil
}
func (s *stubAdapter) Filter(Event) FilterResult { return s.filter }
func (s *stubAdapter) Render(Event) (string, error) {
	if s.renderErr != nil {
		return "", s.renderErr
	}
	return s.rendered, nil
}

func newRouter(t *testing.T, adapters ...Adapter) (*Router, *sync.WaitGroup, *[]Event, *[]string) {
	t.Helper()
	reg := NewRegistry()
	for _, a := range adapters {
		if err := reg.Register(a); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	var mu sync.Mutex
	var events []Event
	var prompts []string
	wg := &sync.WaitGroup{}
	rt := &Router{
		Registry:      reg,
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
		Runner: func(_ context.Context, e Event, p string) {
			defer wg.Done()
			mu.Lock()
			events = append(events, e)
			prompts = append(prompts, p)
			mu.Unlock()
		},
	}
	return rt, wg, &events, &prompts
}

func mkReq(path string, body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/"+path, bytes.NewReader(body))
	req.Header.Set("X-Kind", "widget")
	req.Header.Set("X-Delivery", "d-1")
	return req
}

func TestRouter_404_UnknownAdapter(t *testing.T) {
	t.Parallel()
	rt, _, _, _ := newRouter(t)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, mkReq("nope", []byte(`{}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func TestRouter_403_AdapterDisabled(t *testing.T) {
	t.Parallel()
	a := &stubAdapter{name: "foo", path: "foo", enabled: false}
	rt, _, _, _ := newRouter(t, a)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, mkReq("foo", []byte(`{}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", w.Code)
	}
}

func TestRouter_401_VerifyFails(t *testing.T) {
	t.Parallel()
	a := &stubAdapter{name: "foo", path: "foo", enabled: true, verifyErr: errors.New("bad sig")}
	rt, _, _, _ := newRouter(t, a)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, mkReq("foo", []byte(`{}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestRouter_400_ParseFails(t *testing.T) {
	t.Parallel()
	a := &stubAdapter{name: "foo", path: "foo", enabled: true, parseErr: errors.New("broken body")}
	rt, _, _, _ := newRouter(t, a)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, mkReq("foo", []byte(`{}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func TestRouter_202_Skipped_WhenFilterDenies(t *testing.T) {
	t.Parallel()
	a := &stubAdapter{
		name: "foo", path: "foo", enabled: true,
		filter: FilterResult{Allowed: false, Reason: "event not in allowlist"},
	}
	rt, _, _, _ := newRouter(t, a)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, mkReq("foo", []byte(`{}`)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "skipped" {
		t.Fatalf("status=%v want skipped", resp["status"])
	}
}

func TestRouter_204_OnHealthcheckReason(t *testing.T) {
	t.Parallel()
	a := &stubAdapter{
		name: "foo", path: "foo", enabled: true,
		filter: FilterResult{Allowed: false, Reason: "healthcheck:ping"},
	}
	rt, _, _, _ := newRouter(t, a)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, mkReq("foo", []byte(`{}`)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
}

func TestRouter_202_Accepted_Dispatches(t *testing.T) {
	t.Parallel()
	a := &stubAdapter{
		name: "foo", path: "foo", enabled: true,
		filter:   FilterResult{Allowed: true},
		rendered: "DO THE THING",
	}
	rt, wg, events, prompts := newRouter(t, a)
	wg.Add(1)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, mkReq("foo", []byte(`hello world`)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	wg.Wait()
	if len(*events) != 1 {
		t.Fatalf("runner not invoked once: %d", len(*events))
	}
	ev := (*events)[0]
	if ev.SessionID == "" || !strings.HasPrefix(ev.SessionID, "webhook:foo:widget:") {
		t.Fatalf("session_id=%q", ev.SessionID)
	}
	if (*prompts)[0] != "DO THE THING" {
		t.Fatalf("prompt=%q", (*prompts)[0])
	}
}

func TestRouter_503_OnSaturation(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	a := &stubAdapter{
		name: "foo", path: "foo", enabled: true,
		filter:   FilterResult{Allowed: true},
		rendered: "ok",
	}
	reg := NewRegistry()
	_ = reg.Register(a)
	rt := &Router{
		Registry:      reg,
		MaxConcurrent: 1,
		Timeout:       5 * time.Second,
		Runner: func(_ context.Context, _ Event, _ string) {
			<-release
		},
	}
	// Fill the sole slot.
	w1 := httptest.NewRecorder()
	rt.ServeHTTP(w1, mkReq("foo", []byte(`{}`)))
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first: %d", w1.Code)
	}
	// Let the goroutine acquire its slot.
	time.Sleep(50 * time.Millisecond)
	w2 := httptest.NewRecorder()
	rt.ServeHTTP(w2, mkReq("foo", []byte(`{}`)))
	if w2.Code != http.StatusServiceUnavailable {
		t.Fatalf("second: %d", w2.Code)
	}
	if w2.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After missing")
	}
	close(release)
}

func TestRegistry_DuplicatesReturnError(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	a := &stubAdapter{name: "foo", path: "foo", enabled: true}
	if err := reg.Register(a); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(a); err == nil {
		t.Fatal("expected duplicate name error")
	}
	b := &stubAdapter{name: "bar", path: "foo", enabled: true}
	if err := reg.Register(b); err == nil {
		t.Fatal("expected duplicate path error")
	}
}
