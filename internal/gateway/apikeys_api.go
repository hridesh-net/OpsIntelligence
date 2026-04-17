package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// apikeys_api.go is the HTTP twin of cmd/opsintelligence admin apikey.
// The plaintext token (opi_<id>_<secret>) is returned exactly ONCE in
// the POST response — the UI must render a dialog that forces the
// operator to copy it before closing.
//
// Permission model:
//
//	GET    /api/v1/apikeys                — list ALL (apikeys.read.all)
//	                                     — list own (apikeys.read.own, filter by ?mine=1 or owner implicit)
//	POST   /api/v1/apikeys                — mint (own user: apikeys.manage.own;
//	                                     other user: apikeys.manage.all)
//	DELETE /api/v1/apikeys/{id}           — revoke (owner: manage.own;
//	                                     other: manage.all)

type apiKeyDTO struct {
	ID         string     `json:"id"`
	KeyID      string     `json:"key_id"`
	Name       string     `json:"name"`
	UserID     string     `json:"user_id"`
	Username   string     `json:"username,omitempty"`
	Scopes     []string   `json:"scopes,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	Status     string     `json:"status"` // active|revoked|expired
}

type createAPIKeyRequest struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username"` // alternative to user_id
	Name     string   `json:"name"`
	Expires  string   `json:"expires"` // Go duration; "" means no expiry
	Scopes   []string `json:"scopes"`
}

type createAPIKeyResponse struct {
	Key        apiKeyDTO `json:"key"`
	PlainToken string    `json:"plain_token"` // shown exactly once
}

// ─────────────────────────────────────────────────────────────────────
// routing
// ─────────────────────────────────────────────────────────────────────

// handleAPIKeys serves /api/v1/apikeys.
func (s *AuthService) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listAPIKeys(w, r)
	case http.MethodPost:
		s.createAPIKey(w, r)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAPIKeyItem serves /api/v1/apikeys/{id}.
func (s *AuthService) handleAPIKeyItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/apikeys/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		writeJSONError(w, http.StatusNotFound, "api key id required")
		return
	}
	switch r.Method {
	case http.MethodDelete:
		s.revokeAPIKey(w, r, rest)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ─────────────────────────────────────────────────────────────────────
// list
// ─────────────────────────────────────────────────────────────────────

func (s *AuthService) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)

	mineOnly := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("mine")), "1") ||
		strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("mine")), "true")

	canAll := rbac.Can(p, rbac.PermAPIKeysReadAll)
	canOwn := rbac.Can(p, rbac.PermAPIKeysReadOwn)
	if !canAll && !canOwn {
		writeJSONError(w, http.StatusForbidden, "apikeys.read.own required")
		return
	}

	var keys []datastore.APIKey
	var err error
	if !canAll || mineOnly {
		if p.UserID == "" {
			writeJSONError(w, http.StatusForbidden, "only user principals can list own keys")
			return
		}
		keys, err = s.Store.APIKeys().ListForUser(ctx, p.UserID)
	} else {
		keys, err = s.Store.APIKeys().ListAll(ctx, 500, 0)
	}
	if err != nil {
		s.Log.Error("apikeys list", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "list failed")
		return
	}
	ownersByID := make(map[string]string)
	out := make([]apiKeyDTO, 0, len(keys))
	for i := range keys {
		k := &keys[i]
		username := ownersByID[k.UserID]
		if username == "" {
			if u, uerr := s.Store.Users().Get(ctx, k.UserID); uerr == nil {
				username = u.Username
				ownersByID[k.UserID] = username
			}
		}
		out = append(out, apiKeyRowToDTO(k, username))
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// ─────────────────────────────────────────────────────────────────────
// create
// ─────────────────────────────────────────────────────────────────────

func (s *AuthService) createAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.APIKeysEnabled {
		writeJSONError(w, http.StatusForbidden, "api keys are disabled in config")
		return
	}
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)

	var req createAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}

	// Resolve owner.
	owner, err := s.resolveAPIKeyOwner(ctx, p, req)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if owner.Status != datastore.UserActive {
		writeJSONError(w, http.StatusConflict, "owner account is not active")
		return
	}

	// Permission check: own vs. other.
	if owner.ID == p.UserID {
		if err := rbac.EnforceAny(ctx, p, rbac.PermAPIKeysManageOwn, rbac.PermAPIKeysManageAll); err != nil {
			writeJSONError(w, http.StatusForbidden, "apikeys.manage.own required")
			return
		}
	} else {
		if err := rbac.Enforce(ctx, p, rbac.PermAPIKeysManageAll); err != nil {
			writeJSONError(w, http.StatusForbidden, "apikeys.manage.all required to mint keys for other users")
			return
		}
	}

	// Generate.
	pt, err := auth.GenerateAPIKey(owner.ID, req.Name, req.Scopes)
	if err != nil {
		s.Log.Error("generate api key", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "generate failed")
		return
	}
	if exp := strings.TrimSpace(req.Expires); exp != "" {
		d, perr := time.ParseDuration(exp)
		if perr != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid expires duration")
			return
		}
		t := time.Now().Add(d).UTC()
		pt.Record.ExpiresAt = &t
	}
	if err := s.Store.APIKeys().Create(ctx, pt.Record); err != nil {
		s.Log.Error("apikeys create", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "persist failed")
		return
	}
	_ = s.appendAPIKeysAudit(r, p, "apikey.create", pt.Record.ID, true, nil, map[string]any{
		"owner_id":  owner.ID,
		"owner":     owner.Username,
		"key_id":    pt.Record.KeyID,
		"scopes":    pt.Record.Scopes,
		"expires":   req.Expires,
		"name":      pt.Record.Name,
		"mint_type": mintType(owner.ID == p.UserID),
	})
	writeJSON(w, http.StatusCreated, createAPIKeyResponse{
		Key:        apiKeyRowToDTO(pt.Record, owner.Username),
		PlainToken: pt.PlainToken,
	})
}

// resolveAPIKeyOwner picks the target user for the mint request:
//
//  1. req.UserID if set
//  2. req.Username if set
//  3. the calling principal (self-mint)
func (s *AuthService) resolveAPIKeyOwner(ctx context.Context, p *auth.Principal, req createAPIKeyRequest) (*datastore.User, error) {
	switch {
	case strings.TrimSpace(req.UserID) != "":
		return s.Store.Users().Get(ctx, strings.TrimSpace(req.UserID))
	case strings.TrimSpace(req.Username) != "":
		return s.Store.Users().GetByUsername(ctx, strings.TrimSpace(req.Username))
	default:
		if p == nil || p.UserID == "" {
			return nil, fmt.Errorf("cannot infer owner without user_id/username")
		}
		return s.Store.Users().Get(ctx, p.UserID)
	}
}

// ─────────────────────────────────────────────────────────────────────
// revoke
// ─────────────────────────────────────────────────────────────────────

func (s *AuthService) revokeAPIKey(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	// id can be either the row ID ("ak-<keyid>") or the public key_id.
	k, err := s.findAPIKey(ctx, id)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "api key not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if k.UserID == p.UserID {
		if err := rbac.EnforceAny(ctx, p, rbac.PermAPIKeysManageOwn, rbac.PermAPIKeysManageAll); err != nil {
			writeJSONError(w, http.StatusForbidden, "apikeys.manage.own required")
			return
		}
	} else {
		if err := rbac.Enforce(ctx, p, rbac.PermAPIKeysManageAll); err != nil {
			writeJSONError(w, http.StatusForbidden, "apikeys.manage.all required")
			return
		}
	}
	if err := s.Store.APIKeys().Revoke(ctx, k.ID); err != nil {
		_ = s.appendAPIKeysAudit(r, p, "apikey.revoke", k.ID, false, err, nil)
		writeJSONError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	_ = s.appendAPIKeysAudit(r, p, "apikey.revoke", k.ID, true, nil, map[string]any{
		"owner_id": k.UserID,
		"key_id":   k.KeyID,
		"name":     k.Name,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": k.ID})
}

func (s *AuthService) findAPIKey(ctx context.Context, idOrKeyID string) (*datastore.APIKey, error) {
	idOrKeyID = strings.TrimSpace(idOrKeyID)
	if idOrKeyID == "" {
		return nil, datastore.ErrNotFound
	}
	// ak-<keyid> → strip and look up by key_id.
	keyID := idOrKeyID
	if strings.HasPrefix(idOrKeyID, "ak-") {
		keyID = strings.TrimPrefix(idOrKeyID, "ak-")
	}
	return s.Store.APIKeys().GetByKeyID(ctx, keyID)
}

// ─────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────

func apiKeyRowToDTO(k *datastore.APIKey, username string) apiKeyDTO {
	if k == nil {
		return apiKeyDTO{}
	}
	dto := apiKeyDTO{
		ID:         k.ID,
		KeyID:      k.KeyID,
		Name:       k.Name,
		UserID:     k.UserID,
		Username:   username,
		Scopes:     append([]string(nil), k.Scopes...),
		CreatedAt:  k.CreatedAt,
		LastUsedAt: k.LastUsedAt,
		ExpiresAt:  k.ExpiresAt,
		RevokedAt:  k.RevokedAt,
	}
	switch {
	case k.RevokedAt != nil:
		dto.Status = "revoked"
	case k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()):
		dto.Status = "expired"
	default:
		dto.Status = "active"
	}
	return dto
}

func mintType(isSelf bool) string {
	if isSelf {
		return "self"
	}
	return "delegated"
}

func (s *AuthService) appendAPIKeysAudit(r *http.Request, p *auth.Principal, action, keyRowID string, success bool, err error, extra map[string]any) error {
	if s == nil || s.Store == nil {
		return nil
	}
	meta := map[string]any{
		"path":   r.URL.Path,
		"method": r.Method,
	}
	for k, v := range extra {
		meta[k] = v
	}
	entry := &datastore.AuditEntry{
		ActorType:    actorTypeFromPrincipal(p),
		ActorID:      actorIDFromPrincipal(p),
		Action:       action,
		ResourceType: "apikey",
		ResourceID:   keyRowID,
		RemoteAddr:   r.RemoteAddr,
		UserAgent:    r.UserAgent(),
		Success:      success,
		Metadata:     meta,
	}
	if err != nil {
		entry.ErrorMessage = err.Error()
	}
	return s.Store.Audit().Append(r.Context(), entry)
}
