package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/memory"
)

// ─────────────────────────────────────────────
// A2A task store
// ─────────────────────────────────────────────

// A2ATaskStatus represents the lifecycle state of an A2A task.
type A2ATaskStatus string

const (
	A2ATaskPending   A2ATaskStatus = "pending"
	A2ATaskRunning   A2ATaskStatus = "running"
	A2ATaskCompleted A2ATaskStatus = "completed"
	A2ATaskFailed    A2ATaskStatus = "failed"
	A2ATaskCancelled A2ATaskStatus = "cancelled"
)

type a2aTask struct {
	ID        string        `json:"id"`
	SessionID string        `json:"session_id"`
	Status    A2ATaskStatus `json:"status"`
	Result    string        `json:"result,omitempty"`
	Error     string        `json:"error,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// A2ATaskStore holds running and recently-completed A2A tasks.
type A2ATaskStore struct {
	mu      sync.RWMutex
	tasks   map[string]*a2aTask
	cancels map[string]context.CancelFunc
}

func newA2ATaskStore() *A2ATaskStore {
	return &A2ATaskStore{
		tasks:   make(map[string]*a2aTask),
		cancels: make(map[string]context.CancelFunc),
	}
}

func (s *A2ATaskStore) create(sessionID string) *a2aTask {
	t := &a2aTask{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Status:    A2ATaskPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	s.mu.Lock()
	s.tasks[t.ID] = t
	s.mu.Unlock()
	return t
}

func (s *A2ATaskStore) setRunning(taskID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[taskID]; ok {
		t.Status = A2ATaskRunning
		t.UpdatedAt = time.Now()
	}
	s.cancels[taskID] = cancel
}

func (s *A2ATaskStore) complete(taskID, result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[taskID]; ok {
		t.Status = A2ATaskCompleted
		t.Result = result
		t.UpdatedAt = time.Now()
	}
	delete(s.cancels, taskID)
}

func (s *A2ATaskStore) fail(taskID, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[taskID]; ok {
		t.Status = A2ATaskFailed
		t.Error = errMsg
		t.UpdatedAt = time.Now()
	}
	delete(s.cancels, taskID)
}

func (s *A2ATaskStore) cancel(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cancelFn, ok := s.cancels[taskID]
	if !ok {
		return false
	}
	cancelFn()
	if t, ok2 := s.tasks[taskID]; ok2 {
		t.Status = A2ATaskCancelled
		t.UpdatedAt = time.Now()
	}
	delete(s.cancels, taskID)
	return true
}

func (s *A2ATaskStore) get(taskID string) (*a2aTask, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[taskID]
	return t, ok
}

// ─────────────────────────────────────────────
// JSON-RPC 2.0 wire types
// ─────────────────────────────────────────────

// AgentCard represents the A2A Agent Card structure.
type AgentCard struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	Endpoint     string   `json:"endpoint"`
	Capabilities []string `json:"capabilities"`
	AgentID      string   `json:"agent_id,omitempty"`
}

// JSONRPCRequest represents a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      interface{}     `json:"id"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
	ID      interface{}   `json:"id"`
}

// JSONRPCError represents a JSON-RPC 2.0 error.
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// A2AMessageParams represents the parameters for message/send and message/stream.
type A2AMessageParams struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id,omitempty"`
}

// A2ATaskParams holds the task ID for tasks/get and tasks/cancel.
type A2ATaskParams struct {
	TaskID string `json:"task_id"`
}

// A2ATaskResult is the response payload for message/send and tasks/get.
type A2ATaskResult struct {
	TaskID    string        `json:"task_id"`
	Status    A2ATaskStatus `json:"status"`
	Content   string        `json:"content,omitempty"`
	Error     string        `json:"error,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// ─────────────────────────────────────────────
// Auth helper
// ─────────────────────────────────────────────

// checkA2AAuth validates bearer token on A2A requests when s.Token is set.
// Returns true if the request is authorised (or auth is disabled).
func (s *Server) checkA2AAuth(r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(auth, "Bearer ")
	return ok && token == s.Token
}

// ─────────────────────────────────────────────
// Handlers
// ─────────────────────────────────────────────

func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	name := s.Config.A2A.Name
	if name == "" {
		name = "OpsIntelligence"
	}
	desc := s.Config.A2A.Description
	if desc == "" {
		desc = "Autonomous DevOps agent: PR review, SonarQube triage, CI/CD regression detection, incident response, and runbook execution."
	}

	card := AgentCard{
		Name:        name,
		Description: desc,
		Version:     s.Version,
		AgentID:     s.Config.A2A.AgentID,
		Capabilities: []string{
			"messaging",
			"streaming",
			"task-management",
			"devops.pr-review",
			"devops.sonar-triage",
			"devops.cicd-regression",
			"devops.incident-scribe",
			"smart-prompt-chains",
			"webhooks",
		},
		Endpoint: fmt.Sprintf("http://%s/api/a2a", r.Host),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(card)
}

func (s *Server) handleA2A(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Bearer token auth — reuse the gateway token so operators don't need a separate key.
	if !s.checkA2AAuth(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="opsintelligence-a2a"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendJSONRPCError(w, nil, -32700, "Parse error")
		return
	}

	switch req.Method {
	case "message/send":
		s.handleA2AMessageSend(w, &req)
	case "message/stream":
		s.handleA2AMessageStream(w, &req)
	case "tasks/get":
		s.handleA2ATaskGet(w, &req)
	case "tasks/cancel":
		s.handleA2ATaskCancel(w, &req)
	default:
		s.sendJSONRPCError(w, req.ID, -32601, "Method not found")
	}
}

// handleA2AMessageSend processes message/send synchronously in a tracked task.
// The response includes a task_id so the caller can reference it via tasks/get.
func (s *Server) handleA2AMessageSend(w http.ResponseWriter, req *JSONRPCRequest) {
	var params A2AMessageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendJSONRPCError(w, req.ID, -32602, "Invalid params")
		return
	}

	sessionID := params.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	store := s.taskStore()
	task := store.create(sessionID)

	taskCtx, cancel := context.WithCancel(context.Background())
	store.setRunning(task.ID, cancel)

	sessionRunner := s.Runner.WithSession(sessionID)
	res, err := sessionRunner.Run(taskCtx, memory.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      memory.RoleUser,
		Content:   params.Message,
		CreatedAt: time.Now(),
	})

	var result A2ATaskResult
	if err != nil {
		store.fail(task.ID, err.Error())
		result = A2ATaskResult{
			TaskID:    task.ID,
			Status:    A2ATaskFailed,
			Error:     err.Error(),
			CreatedAt: task.CreatedAt,
			UpdatedAt: time.Now(),
		}
	} else {
		store.complete(task.ID, res.Response)
		result = A2ATaskResult{
			TaskID:    task.ID,
			Status:    A2ATaskCompleted,
			Content:   res.Response,
			CreatedAt: task.CreatedAt,
			UpdatedAt: time.Now(),
		}
	}

	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleA2AMessageStream processes message/stream and tracks the task for cancellation.
func (s *Server) handleA2AMessageStream(w http.ResponseWriter, req *JSONRPCRequest) {
	var params A2AMessageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendJSONRPCError(w, req.ID, -32602, "Invalid params")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	sessionID := params.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	store := s.taskStore()
	task := store.create(sessionID)
	taskCtx, cancel := context.WithCancel(context.Background())
	store.setRunning(task.ID, cancel)

	// Send the task ID as the first event so the caller can use tasks/cancel.
	taskIDPayload, _ := json.Marshal(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]string{
			"type":    "task_id",
			"task_id": task.ID,
		},
	})
	fmt.Fprintf(w, "data: %s\n\n", taskIDPayload)
	flusher.Flush()

	sessionRunner := s.Runner.WithSession(sessionID)
	done := make(chan struct{})
	handler := &a2aSSEHandler{
		w:       w,
		flusher: flusher,
		done:    done,
		id:      req.ID,
		taskID:  task.ID,
		store:   store,
	}

	go func() {
		sessionRunner.RunStream(taskCtx, memory.Message{
			ID:        uuid.New().String(),
			SessionID: sessionID,
			Role:      memory.RoleUser,
			Content:   params.Message,
			CreatedAt: time.Now(),
		}, handler)
	}()

	select {
	case <-done:
	case <-taskCtx.Done():
	}
}

// handleA2ATaskGet returns the status and result of a task by ID.
func (s *Server) handleA2ATaskGet(w http.ResponseWriter, req *JSONRPCRequest) {
	var params A2ATaskParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendJSONRPCError(w, req.ID, -32602, "Invalid params: task_id required")
		return
	}
	if params.TaskID == "" {
		s.sendJSONRPCError(w, req.ID, -32602, "task_id is required")
		return
	}

	task, ok := s.taskStore().get(params.TaskID)
	if !ok {
		s.sendJSONRPCError(w, req.ID, -32001, fmt.Sprintf("task %q not found", params.TaskID))
		return
	}

	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: A2ATaskResult{
			TaskID:    task.ID,
			Status:    task.Status,
			Content:   task.Result,
			Error:     task.Error,
			CreatedAt: task.CreatedAt,
			UpdatedAt: task.UpdatedAt,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleA2ATaskCancel cancels a running task.
func (s *Server) handleA2ATaskCancel(w http.ResponseWriter, req *JSONRPCRequest) {
	var params A2ATaskParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendJSONRPCError(w, req.ID, -32602, "Invalid params: task_id required")
		return
	}
	if params.TaskID == "" {
		s.sendJSONRPCError(w, req.ID, -32602, "task_id is required")
		return
	}

	if !s.taskStore().cancel(params.TaskID) {
		s.sendJSONRPCError(w, req.ID, -32001, fmt.Sprintf("task %q not found or already finished", params.TaskID))
		return
	}

	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]string{"status": string(A2ATaskCancelled), "task_id": params.TaskID},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) taskStore() *A2ATaskStore {
	if s.A2ATasks == nil {
		s.A2ATasks = newA2ATaskStore()
	}
	return s.A2ATasks
}

func (s *Server) sendJSONRPCError(w http.ResponseWriter, id interface{}, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ─────────────────────────────────────────────
// SSE streaming handler
// ─────────────────────────────────────────────

type a2aSSEHandler struct {
	w       http.ResponseWriter
	flusher http.Flusher
	done    chan struct{}
	id      interface{}
	taskID  string
	store   *A2ATaskStore
}

func (h *a2aSSEHandler) OnToken(token string) {
	payload, _ := json.Marshal(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      h.id,
		Result: map[string]string{
			"type":    "token",
			"content": token,
		},
	})
	fmt.Fprintf(h.w, "data: %s\n\n", string(payload))
	h.flusher.Flush()
}

func (h *a2aSSEHandler) OnToolCall(name string, _ json.RawMessage) {}
func (h *a2aSSEHandler) OnToolResult(name string, _ string)        {}

func (h *a2aSSEHandler) OnDone(res *agent.RunResult) {
	if h.store != nil && h.taskID != "" {
		content := ""
		if res != nil {
			content = res.Response
		}
		h.store.complete(h.taskID, content)
	}
	payload, _ := json.Marshal(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      h.id,
		Result: map[string]string{
			"type":    "done",
			"task_id": h.taskID,
		},
	})
	fmt.Fprintf(h.w, "data: %s\n\n", string(payload))
	h.flusher.Flush()
	close(h.done)
}

func (h *a2aSSEHandler) OnError(err error) {
	if h.store != nil && h.taskID != "" {
		h.store.fail(h.taskID, err.Error())
	}
	payload, _ := json.Marshal(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      h.id,
		Error: &JSONRPCError{
			Code:    -32000,
			Message: err.Error(),
		},
	})
	fmt.Fprintf(h.w, "data: %s\n\n", string(payload))
	h.flusher.Flush()
	close(h.done)
}
