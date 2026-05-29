package gateway

// kanban_api.go — REST endpoints for the Kanban board system.
//
// Endpoints:
//   GET    /api/v1/boards                    List boards
//   POST   /api/v1/boards                    Create board
//   GET    /api/v1/boards/{id}               Get board + columns + cards
//   PUT    /api/v1/boards/{id}               Update board
//   DELETE /api/v1/boards/{id}               Delete board
//   GET    /api/v1/boards/{id}/columns       List columns
//   POST   /api/v1/boards/{id}/columns       Create column
//   PUT    /api/v1/boards/{id}/columns/{cid} Update column
//   DELETE /api/v1/boards/{id}/columns/{cid} Delete column
//   GET    /api/v1/boards/{id}/cards         List cards
//   POST   /api/v1/boards/{id}/cards         Create card
//   GET    /api/v1/boards/{id}/cards/{cid}   Get card detail
//   PUT    /api/v1/boards/{id}/cards/{cid}   Update card
//   DELETE /api/v1/boards/{id}/cards/{cid}   Delete card
//   POST   /api/v1/boards/{id}/cards/{cid}/move   Move card to column
//   GET    /api/v1/boards/{id}/cards/{cid}/runs   List card runs
//   POST   /api/v1/boards/{id}/cards/{cid}/dispatch  Dispatch agent on card
//   GET    /api/v1/runs/{rid}                Get run detail + events
//   POST   /api/v1/runs/{rid}/stop           Stop a run
//   POST   /api/v1/runs/{rid}/decisions/{did}  Answer a pending decision
//   GET    /api/v1/boards/{id}/agents        List board agents
//   POST   /api/v1/boards/{id}/agents        Register agent
//   GET    /api/v1/personas                 List personas
//   POST   /api/v1/personas                 Create persona

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/kanban"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// ── Request / Response types ──────────────────────────────────────────

type createBoardRequest struct {
	Name     string `json:"name"`
	TeamID   string `json:"team_id,omitempty"`
	RepoURL  string `json:"repo_url,omitempty"`
	RepoPath string `json:"repo_path,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

type createColumnRequest struct {
	Name     string `json:"name"`
	Position int    `json:"position"`
	Color    string `json:"color,omitempty"`
	WIPLimit *int   `json:"wip_limit,omitempty"`
}

type createCardRequest struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	CardType    string `json:"card_type,omitempty"`
	Priority    string `json:"priority,omitempty"`
	Effort      string `json:"effort,omitempty"`
}

type updateCardRequest struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	CardType    string `json:"card_type,omitempty"`
	Priority    string `json:"priority,omitempty"`
	Effort      string `json:"effort,omitempty"`
	Status      string `json:"status,omitempty"`
	Assignee    string `json:"assignee,omitempty"`
}

type moveCardRequest struct {
	ColumnID string `json:"column_id"`
}

type dispatchRequest struct {
	AgentID   string `json:"agent_id"`
	PersonaID string `json:"persona_id,omitempty"`
	Model     string `json:"model,omitempty"`
}

type createAgentRequest struct {
	Name       string         `json:"name"`
	AgentType  string         `json:"agent_type"`
	ProviderID string         `json:"provider_id,omitempty"`
	Config     map[string]any `json:"config,omitempty"`
	IsDefault  bool           `json:"is_default,omitempty"`
}

type createPersonaRequest struct {
	Name         string `json:"name"`
	Icon         string `json:"icon,omitempty"`
	Description  string `json:"description,omitempty"`
	SystemPrompt string `json:"system_prompt"`
}

type answerDecisionRequest struct {
	Answer string `json:"answer"`
}

// ── Route dispatcher ──────────────────────────────────────────────────

// HandleKanban dispatches /api/v1/boards, /api/v1/runs, and /api/v1/personas.
func (s *AuthService) HandleKanban(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	path = strings.TrimPrefix(path, "/")

	switch {
	case path == "boards" || path == "boards/":
		s.handleBoards(w, r)
	case strings.HasPrefix(path, "boards/"):
		s.handleBoardDetail(w, r, strings.TrimPrefix(path, "boards/"))
	case strings.HasPrefix(path, "runs/"):
		s.handleRunDetail(w, r, strings.TrimPrefix(path, "runs/"))
	case path == "personas" || path == "personas/":
		s.handlePersonas(w, r)
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

// ── Boards ────────────────────────────────────────────────────────────

func (s *AuthService) handleBoards(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		boards, err := s.Store.Boards().List(r.Context(), datastore.BoardFilter{Limit: 100})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"boards": boards})
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createBoardRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "name required")
			return
		}
		mode := req.Mode
		if mode == "" {
			mode = "local"
		}
		board := &datastore.Board{
			Name:     req.Name,
			TeamID:   req.TeamID,
			RepoURL:  req.RepoURL,
			RepoPath: req.RepoPath,
			Mode:     mode,
		}
		if err := s.Store.Boards().Create(r.Context(), board); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Seed default columns
		defaults := []struct{ name string; pos int }{
			{"Inbox", 0}, {"Backlog", 1}, {"Todo", 2}, {"In Progress", 3}, {"Review", 4}, {"Done", 5},
		}
		for _, d := range defaults {
			col := &datastore.BoardColumn{BoardID: board.ID, Name: d.name, Position: d.pos}
			_ = s.Store.BoardColumns().Create(r.Context(), col)
		}
		writeJSON(w, http.StatusCreated, board)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *AuthService) handleBoardDetail(w http.ResponseWriter, r *http.Request, suffix string) {
	parts := strings.SplitN(suffix, "/", 3)
	boardID := parts[0]
	if boardID == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	// No sub-resource: /api/v1/boards/{id}
	if len(parts) == 1 || parts[1] == "" {
		s.handleSingleBoard(w, r, boardID)
		return
	}

	sub := parts[1]
	switch sub {
	case "columns":
		if len(parts) == 2 || parts[2] == "" {
			s.handleBoardColumns(w, r, boardID)
		} else {
			s.handleSingleColumn(w, r, boardID, parts[2])
		}
	case "cards":
		if len(parts) == 2 || parts[2] == "" {
			s.handleBoardCards(w, r, boardID)
		} else {
			s.handleCardDetail(w, r, boardID, parts[2])
		}
	case "agents":
		s.handleBoardAgents(w, r, boardID)
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

func (s *AuthService) handleSingleBoard(w http.ResponseWriter, r *http.Request, boardID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		board, err := s.Store.Boards().Get(r.Context(), boardID)
		if err != nil {
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "board not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		columns, _ := s.Store.BoardColumns().ListByBoard(r.Context(), boardID)
		cards, _ := s.Store.BoardCards().List(r.Context(), datastore.BoardCardFilter{BoardID: boardID, Limit: 1000})
		writeJSON(w, http.StatusOK, map[string]any{
			"board":   board,
			"columns": columns,
			"cards":   cards,
		})
	case http.MethodPut:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createBoardRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		board, err := s.Store.Boards().Get(r.Context(), boardID)
		if err != nil {
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "board not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if req.Name != "" {
			board.Name = req.Name
		}
		if req.RepoURL != "" {
			board.RepoURL = req.RepoURL
		}
		if req.RepoPath != "" {
			board.RepoPath = req.RepoPath
		}
		if req.Mode != "" {
			board.Mode = req.Mode
		}
		if err := s.Store.Boards().Update(r.Context(), board); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, board)
	case http.MethodDelete:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if err := s.Store.Boards().Delete(r.Context(), boardID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusNoContent, nil)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Columns ───────────────────────────────────────────────────────────

func (s *AuthService) handleBoardColumns(w http.ResponseWriter, r *http.Request, boardID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		cols, err := s.Store.BoardColumns().ListByBoard(r.Context(), boardID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"columns": cols})
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createColumnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "name required")
			return
		}
		col := &datastore.BoardColumn{
			BoardID:  boardID,
			Name:     req.Name,
			Position: req.Position,
			Color:    req.Color,
			WIPLimit: req.WIPLimit,
		}
		if err := s.Store.BoardColumns().Create(r.Context(), col); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, col)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *AuthService) handleSingleColumn(w http.ResponseWriter, r *http.Request, boardID, columnID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodPut:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createColumnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		col, err := s.Store.BoardColumns().Get(r.Context(), columnID)
		if err != nil {
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "column not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if col.BoardID != boardID {
			writeJSONError(w, http.StatusNotFound, "column not found")
			return
		}
		if req.Name != "" {
			col.Name = req.Name
		}
		col.Position = req.Position
		col.Color = req.Color
		col.WIPLimit = req.WIPLimit
		if err := s.Store.BoardColumns().Update(r.Context(), col); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, col)
	case http.MethodDelete:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if err := s.Store.BoardColumns().Delete(r.Context(), columnID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusNoContent, nil)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Cards ─────────────────────────────────────────────────────────────

func (s *AuthService) handleBoardCards(w http.ResponseWriter, r *http.Request, boardID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		q := r.URL.Query()
		filter := datastore.BoardCardFilter{
			BoardID:  boardID,
			ColumnID: q.Get("column_id"),
			Status:   q.Get("status"),
			Limit:    parseLimit(q.Get("limit"), 100),
			Offset:   parseInt(q.Get("offset")),
		}
		cards, err := s.Store.BoardCards().List(r.Context(), filter)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cards": cards})
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermCardsCreate); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createCardRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Title == "" {
			writeJSONError(w, http.StatusBadRequest, "title required")
			return
		}
		// Place new cards in the first column (Inbox, position 0)
		cols, err := s.Store.BoardColumns().ListByBoard(r.Context(), boardID)
		if err != nil || len(cols) == 0 {
			writeJSONError(w, http.StatusInternalServerError, "no columns found")
			return
		}
		columnID := cols[0].ID
		cardType := req.CardType
		if cardType == "" {
			cardType = "feature"
		}
		priority := req.Priority
		if priority == "" {
			priority = "p2"
		}
		card := &datastore.BoardCard{
			BoardID:     boardID,
			ColumnID:    columnID,
			Title:       req.Title,
			Description: req.Description,
			CardType:    cardType,
			Priority:    priority,
			Effort:      req.Effort,
		}
		if err := s.Store.BoardCards().Create(r.Context(), card); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, card)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *AuthService) handleCardDetail(w http.ResponseWriter, r *http.Request, boardID, cardSuffix string) {
	parts := strings.SplitN(cardSuffix, "/", 2)
	cardID := parts[0]
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}

	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if sub == "" || sub == "/" {
			card, err := s.Store.BoardCards().Get(r.Context(), cardID)
			if err != nil {
				if isNotFound(err) {
					writeJSONError(w, http.StatusNotFound, "card not found")
					return
				}
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			runs, _ := s.Store.CardRuns().List(r.Context(), datastore.CardRunFilter{CardID: cardID, Limit: 50})
			writeJSON(w, http.StatusOK, map[string]any{"card": card, "runs": runs})
			return
		}
		if sub == "runs" || sub == "runs/" {
			runs, err := s.Store.CardRuns().List(r.Context(), datastore.CardRunFilter{CardID: cardID, Limit: 50})
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
			return
		}
		writeJSONError(w, http.StatusNotFound, "not found")
	case http.MethodPut:
		if err := rbac.Enforce(r.Context(), p, rbac.PermCardsEdit); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req updateCardRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		card, err := s.Store.BoardCards().Get(r.Context(), cardID)
		if err != nil {
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "card not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if req.Title != "" {
			card.Title = req.Title
		}
		if req.Description != "" {
			card.Description = req.Description
		}
		if req.CardType != "" {
			card.CardType = req.CardType
		}
		if req.Priority != "" {
			card.Priority = req.Priority
		}
		if req.Effort != "" {
			card.Effort = req.Effort
		}
		if req.Status != "" {
			card.Status = req.Status
		}
		if req.Assignee != "" {
			card.Assignee = req.Assignee
		}
		if err := s.Store.BoardCards().Update(r.Context(), card); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, card)
	case http.MethodDelete:
		if err := rbac.Enforce(r.Context(), p, rbac.PermCardsDelete); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if err := s.Store.BoardCards().Delete(r.Context(), cardID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusNoContent, nil)
	case http.MethodPost:
		if sub == "move" || sub == "move/" {
			if err := rbac.Enforce(r.Context(), p, rbac.PermCardsEdit); err != nil {
				writeJSONError(w, http.StatusForbidden, "permission denied")
				return
			}
			var req moveCardRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			if req.ColumnID == "" {
				writeJSONError(w, http.StatusBadRequest, "column_id required")
				return
			}
			// Enforce WIP limit
			col, err := s.Store.BoardColumns().Get(r.Context(), req.ColumnID)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid column")
				return
			}
			if col.WIPLimit != nil && *col.WIPLimit > 0 {
				cardsInCol, err := s.Store.BoardCards().List(r.Context(), datastore.BoardCardFilter{ColumnID: req.ColumnID})
				if err == nil && len(cardsInCol) >= *col.WIPLimit {
					writeJSONError(w, http.StatusConflict, fmt.Sprintf("WIP limit (%d) reached for column %q", *col.WIPLimit, col.Name))
					return
				}
			}
			if err := s.Store.BoardCards().Move(r.Context(), cardID, req.ColumnID); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"moved": true})
			return
		}
		if sub == "dispatch" || sub == "dispatch/" {
			if err := rbac.Enforce(r.Context(), p, rbac.PermRunsDispatch); err != nil {
				writeJSONError(w, http.StatusForbidden, "permission denied")
				return
			}
			var req dispatchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			if s.Kanban == nil {
				writeJSONError(w, http.StatusServiceUnavailable, "kanban dispatch service not configured")
				return
			}
			run, err := s.Kanban.Dispatch(r.Context(), cardID, kanban.DispatchOpts{
				AgentID:   req.AgentID,
				PersonaID: req.PersonaID,
				Model:     req.Model,
			})
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusAccepted, run)
			return
		}
		writeJSONError(w, http.StatusNotFound, "not found")
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Runs ──────────────────────────────────────────────────────────────

func (s *AuthService) handleRunDetail(w http.ResponseWriter, r *http.Request, suffix string) {
	parts := strings.SplitN(suffix, "/", 3)
	if len(parts) < 1 || parts[0] == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	runID := parts[0]
	p := auth.PrincipalFrom(r.Context())

	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermRunsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		run, err := s.Store.CardRuns().Get(r.Context(), runID)
		if err != nil {
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "run not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		events, _ := s.Store.CardRunEvents().List(r.Context(), datastore.CardRunEventFilter{RunID: runID, Limit: 500})
		decisions, _ := s.Store.PendingDecisions().ListByRun(r.Context(), runID)
		writeJSON(w, http.StatusOK, map[string]any{
			"run":       run,
			"events":    events,
			"decisions": decisions,
		})
	case http.MethodPost:
		if len(parts) < 2 {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		sub := parts[1]
		if sub == "stop" || sub == "stop/" {
			if err := rbac.Enforce(r.Context(), p, rbac.PermRunsCancel); err != nil {
				writeJSONError(w, http.StatusForbidden, "permission denied")
				return
			}
			if s.Kanban != nil {
				if err := s.Kanban.StopRun(r.Context(), runID); err != nil {
					if isNotFound(err) {
						writeJSONError(w, http.StatusNotFound, "run not found")
						return
					}
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
			} else {
				// Fallback: just mark as stopped in DB.
				run, err := s.Store.CardRuns().Get(r.Context(), runID)
				if err != nil {
					if isNotFound(err) {
						writeJSONError(w, http.StatusNotFound, "run not found")
						return
					}
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
				run.Status = "stopped"
				run.CompletedAt = &[]time.Time{time.Now().UTC()}[0]
				if err := s.Store.CardRuns().Update(r.Context(), run); err != nil {
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"stopped": true})
			return
		}
		if sub == "decisions" && len(parts) >= 3 {
			decisionID := parts[2]
			if err := rbac.Enforce(r.Context(), p, rbac.PermRunsDispatch); err != nil {
				writeJSONError(w, http.StatusForbidden, "permission denied")
				return
			}
			var req answerDecisionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			if s.Kanban != nil {
				if err := s.Kanban.AnswerDecision(r.Context(), decisionID, req.Answer); err != nil {
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
			} else {
				if err := s.Store.PendingDecisions().Answer(r.Context(), decisionID, req.Answer); err != nil {
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"answered": true})
			return
		}
		writeJSONError(w, http.StatusNotFound, "not found")
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Board Agents ──────────────────────────────────────────────────────

func (s *AuthService) handleBoardAgents(w http.ResponseWriter, r *http.Request, boardID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		agents, err := s.Store.BoardAgents().ListByBoard(r.Context(), boardID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" || req.AgentType == "" {
			writeJSONError(w, http.StatusBadRequest, "name and agent_type required")
			return
		}
		agent := &datastore.BoardAgent{
			BoardID:    boardID,
			Name:       req.Name,
			AgentType:  req.AgentType,
			ProviderID: req.ProviderID,
			Config:     req.Config,
			IsDefault:  req.IsDefault,
			IsActive:   true,
		}
		if err := s.Store.BoardAgents().Create(r.Context(), agent); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, agent)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Personas ──────────────────────────────────────────────────────────

func (s *AuthService) handlePersonas(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermPersonasRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		personas, err := s.Store.Personas().List(r.Context(), datastore.PersonaFilter{Limit: 200})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"personas": personas})
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermPersonasManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createPersonaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" || req.SystemPrompt == "" {
			writeJSONError(w, http.StatusBadRequest, "name and system_prompt required")
			return
		}
		persona := &datastore.Persona{
			ID:           uuid.NewString(),
			Name:         req.Name,
			Icon:         req.Icon,
			Description:  req.Description,
			SystemPrompt: req.SystemPrompt,
			CreatedBy:    p.UserID,
		}
		if err := s.Store.Personas().Create(r.Context(), persona); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, persona)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Helpers ───────────────────────────────────────────────────────────

func isNotFound(err error) bool {
	return err != nil && err.Error() == datastore.ErrNotFound.Error()
}

func parseLimit(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return def
	}
	return v
}

func parseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}
