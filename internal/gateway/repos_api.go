package gateway

// repos_api.go — REST endpoints for the Repo Intelligence feature.
//
// All endpoints are auth-gated by the gateway's standard Bearer check.
//
//   GET  /api/v1/repos               List all registered repos with status.
//   GET  /api/v1/repos/{id}          Get a single repo entry.
//   POST /api/v1/repos               Add a repo.
//   DELETE /api/v1/repos/{id}        Remove a repo.
//   POST /api/v1/repos/{id}/sync     Queue a re-index + re-scan.
//   GET  /api/v1/repos/{id}/memory   Return the repo's LLM-extracted memory.
//   GET  /api/v1/repos/{id}/scan     Return the latest scan result.
//   GET  /api/v1/repos/{id}/users    List users for a repo.
//   POST /api/v1/repos/{id}/users    Add a user to a repo.
//   DELETE /api/v1/repos/{id}/users/{handle}  Remove a user.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/repointel"
)

// RepoIntelAdapter wraps a *repointel.Manager and exposes it as HTTP handlers.
// It is stored on Server.RepoIntel and is nil when the feature is disabled.
type RepoIntelAdapter struct {
	mgr *repointel.Manager
}

// NewRepoIntelAdapter wraps a Manager. Returns nil when mgr is nil.
func NewRepoIntelAdapter(mgr *repointel.Manager) *RepoIntelAdapter {
	if mgr == nil {
		return nil
	}
	return &RepoIntelAdapter{mgr: mgr}
}

// ── Route dispatcher ──────────────────────────────────────────────────────────

// HandleRepos dispatches /api/v1/repos and /api/v1/repos/* requests.
func (a *RepoIntelAdapter) HandleRepos(w http.ResponseWriter, r *http.Request) {
	// Strip /api/v1/repos prefix.
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos")
	path = strings.TrimPrefix(path, "/")

	// /api/v1/repos  (no trailing segment)
	if path == "" || path == "/" {
		switch r.Method {
		case http.MethodGet:
			a.handleList(w, r)
		case http.MethodPost:
			a.handleAdd(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	// /api/v1/repos/progress
	if path == "progress" {
		if r.Method == http.MethodGet {
			a.handleProgress(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Split off the first segment (repo ID) and the rest.
	parts := strings.SplitN(path, "/", 3)
	repoID := urlDecodeSegment(parts[0])

	if len(parts) == 1 {
		// /api/v1/repos/{id}
		switch r.Method {
		case http.MethodGet:
			a.handleGet(w, r, repoID)
		case http.MethodDelete:
			a.handleRemove(w, r, repoID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	sub := parts[1]
	switch sub {
	case "sync":
		if r.Method == http.MethodPost {
			a.handleSync(w, r, repoID)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case "memory":
		if r.Method == http.MethodGet {
			a.handleMemory(w, r, repoID)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case "scan":
		if r.Method == http.MethodGet {
			a.handleScan(w, r, repoID)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case "users":
		a.handleUsers(w, r, repoID, parts)
	default:
		http.NotFound(w, r)
	}
}

// ── /api/v1/repos — list ─────────────────────────────────────────────────────

type repoListItem struct {
	ID          string `json:"id"`
	Platform    string `json:"platform"`
	FullName    string `json:"full_name"`
	Language    string `json:"language,omitempty"`
	IndexStatus string `json:"index_status"`
	ScanStatus  string `json:"scan_status"`
	RiskLevel   string `json:"risk_level,omitempty"`
	UserCount   int    `json:"user_count"`
}

func (a *RepoIntelAdapter) handleList(w http.ResponseWriter, _ *http.Request) {
	entries := a.mgr.ListRepos()
	items := make([]repoListItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, repoListItem{
			ID:          e.ID,
			Platform:    e.Platform,
			FullName:    e.FullName,
			Language:    e.Language,
			IndexStatus: string(e.IndexStatus),
			ScanStatus:  string(e.ScanStatus),
			RiskLevel:   e.RiskLevel,
			UserCount:   len(e.Users),
		})
	}
	repoWriteJSON(w, http.StatusOK, map[string]any{
		"repos": items,
		"total": len(items),
	})
}

// ── /api/v1/repos — add ──────────────────────────────────────────────────────

type addRepoRequest struct {
	Platform string `json:"platform"`
	Owner    string `json:"owner"`
	Name     string `json:"name"`
}

func (a *RepoIntelAdapter) handleAdd(w http.ResponseWriter, r *http.Request) {
	var req addRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Owner == "" || req.Name == "" {
		http.Error(w, "owner and name are required", http.StatusBadRequest)
		return
	}
	if req.Platform == "" {
		req.Platform = "github"
	}
	entry := repointel.RepoEntry{
		ID:       repointel.RepoID(req.Platform, req.Owner, req.Name),
		Platform: req.Platform,
		Owner:    req.Owner,
		Name:     req.Name,
		FullName: req.Owner + "/" + req.Name,
	}
	if err := a.mgr.AddRepo(entry); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	e, _ := a.mgr.GetRepo(entry.ID)
	repoWriteJSON(w, http.StatusCreated, e)
}

// ── /api/v1/repos/{id} — get ─────────────────────────────────────────────────

func (a *RepoIntelAdapter) handleGet(w http.ResponseWriter, _ *http.Request, id string) {
	e, err := a.mgr.GetRepo(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	repoWriteJSON(w, http.StatusOK, e)
}

// ── /api/v1/repos/{id} — remove ──────────────────────────────────────────────

func (a *RepoIntelAdapter) handleRemove(w http.ResponseWriter, _ *http.Request, id string) {
	if err := a.mgr.RemoveRepo(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── /api/v1/repos/{id}/sync ───────────────────────────────────────────────────

func (a *RepoIntelAdapter) handleSync(w http.ResponseWriter, _ *http.Request, id string) {
	if err := a.mgr.SyncRepo(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	repoWriteJSON(w, http.StatusAccepted, map[string]string{"status": "queued", "repo_id": id})
}

// ── /api/v1/repos/{id}/memory ─────────────────────────────────────────────────

func (a *RepoIntelAdapter) handleMemory(w http.ResponseWriter, _ *http.Request, id string) {
	mem, err := a.mgr.LoadMemory(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if mem == nil {
		http.Error(w, "repo not yet indexed", http.StatusNotFound)
		return
	}
	repoWriteJSON(w, http.StatusOK, mem)
}

// ── /api/v1/repos/{id}/scan ───────────────────────────────────────────────────

func (a *RepoIntelAdapter) handleScan(w http.ResponseWriter, _ *http.Request, id string) {
	scan, err := a.mgr.LoadScan(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if scan == nil {
		http.Error(w, "repo not yet scanned", http.StatusNotFound)
		return
	}
	repoWriteJSON(w, http.StatusOK, scan)
}

// ── /api/v1/repos/progress ─────────────────────────────────────────────────────

func (a *RepoIntelAdapter) handleProgress(w http.ResponseWriter, _ *http.Request) {
	state := a.mgr.ProgressSnapshot()
	repoWriteJSON(w, http.StatusOK, map[string]any{
		"progress": state,
		"total":    len(state),
	})
}

// ── /api/v1/repos/{id}/users ──────────────────────────────────────────────────

func (a *RepoIntelAdapter) handleUsers(w http.ResponseWriter, r *http.Request, repoID string, parts []string) {
	// parts[0]=repoID, parts[1]="users", parts[2]=handle (optional)
	if len(parts) < 3 || parts[2] == "" {
		// /users (no handle segment)
		switch r.Method {
		case http.MethodGet:
			a.handleUsersList(w, repoID)
		case http.MethodPost:
			a.handleUsersAdd(w, r, repoID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	handle := urlDecodeSegment(parts[2])
	if r.Method == http.MethodDelete {
		if err := a.mgr.RemoveUser(repoID, handle); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	} else {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *RepoIntelAdapter) handleUsersList(w http.ResponseWriter, repoID string) {
	e, err := a.mgr.GetRepo(repoID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	repoWriteJSON(w, http.StatusOK, map[string]any{"users": e.Users})
}

type addUserRequest struct {
	Handle string `json:"handle"`
	Role   string `json:"role"`
	Email  string `json:"email,omitempty"`
}

func (a *RepoIntelAdapter) handleUsersAdd(w http.ResponseWriter, r *http.Request, repoID string) {
	var req addUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Handle == "" {
		http.Error(w, "handle is required", http.StatusBadRequest)
		return
	}
	if req.Role == "" {
		req.Role = "contributor"
	}
	u := repointel.RepoUser{
		Handle: req.Handle,
		Role:   repointel.UserRole(req.Role),
		Email:  req.Email,
	}
	if err := a.mgr.AddUser(repoID, u); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	repoWriteJSON(w, http.StatusCreated, u)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func repoWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func urlDecodeSegment(s string) string {
	// Simple percent-decode for the colon in "github:owner/name" which gets
	// encoded as "github%3Aowner%2Fname" in URL paths.
	s = strings.ReplaceAll(s, "%3A", ":")
	s = strings.ReplaceAll(s, "%2F", "/")
	s = strings.ReplaceAll(s, "%3a", ":")
	s = strings.ReplaceAll(s, "%2f", "/")
	return s
}
