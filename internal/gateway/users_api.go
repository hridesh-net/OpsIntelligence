package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// users_api.go is the HTTP twin of cmd/opsintelligence admin user /
// role commands. Every mutation writes an audit entry; every read
// enforces rbac.PermUsersRead; every write enforces
// rbac.PermUsersManage (and secrets.write when seeding / resetting a
// password for someone other than the caller).
//
// Custom-role CRUD is intentionally absent — built-in roles cover the
// matrix the dashboard needs today. roles_api.go exposes roles as
// read-only for now so the UI can render the "Assign role" picker.

// ─────────────────────────────────────────────────────────────────────
// DTOs
// ─────────────────────────────────────────────────────────────────────

type userDTO struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	Email       string     `json:"email,omitempty"`
	DisplayName string     `json:"display_name,omitempty"`
	Status      string     `json:"status"`
	OIDCSubject string     `json:"oidc_subject,omitempty"`
	OIDCIssuer  string     `json:"oidc_issuer,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	Roles       []string   `json:"roles,omitempty"`
}

type createUserRequest struct {
	Username    string   `json:"username"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Password    string   `json:"password"`
	Roles       []string `json:"roles"` // role names or `role-<short>` IDs
}

type patchUserRequest struct {
	// Nil means "do not change".
	Email       *string `json:"email,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Status      *string `json:"status,omitempty"` // active|disabled|invited
	Password    *string `json:"password,omitempty"`
}

type grantRoleRequest struct {
	Role string `json:"role"` // role id OR role name ("role-owner" or "owner")
}

type roleDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	IsBuiltIn   bool     `json:"is_builtin"`
	Permissions []string `json:"permissions,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────
// routing
// ─────────────────────────────────────────────────────────────────────

// handleUsers serves /api/v1/users (GET list, POST create).
func (s *AuthService) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listUsers(w, r)
	case http.MethodPost:
		s.createUser(w, r)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleUserSubtree serves /api/v1/users/{id}[/<sub>[/<sub-id>]].
func (s *AuthService) handleUserSubtree(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	userID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.getUser(w, r, userID)
		case http.MethodPatch:
			s.patchUser(w, r, userID)
		case http.MethodDelete:
			s.deleteUser(w, r, userID)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	switch parts[1] {
	case "roles":
		if len(parts) == 2 {
			switch r.Method {
			case http.MethodGet:
				s.listUserRoles(w, r, userID)
			case http.MethodPost:
				s.grantUserRole(w, r, userID)
			default:
				writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}
		if len(parts) == 3 && r.Method == http.MethodDelete {
			s.revokeUserRole(w, r, userID, parts[2])
			return
		}
	}
	writeJSONError(w, http.StatusNotFound, "not found")
}

// ─────────────────────────────────────────────────────────────────────
// users
// ─────────────────────────────────────────────────────────────────────

func (s *AuthService) listUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	if err := rbac.Enforce(ctx, p, rbac.PermUsersRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	users, err := s.Store.Users().List(ctx, 500, 0)
	if err != nil {
		s.Log.Error("users list", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "users list failed")
		return
	}
	out := make([]userDTO, 0, len(users))
	for i := range users {
		u := &users[i]
		dto := userToDTO(u)
		if roles, rerr := s.Store.Roles().ListRolesForUser(ctx, u.ID); rerr == nil {
			dto.Roles = roleNames(roles)
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *AuthService) createUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	if err := rbac.Enforce(ctx, p, rbac.PermUsersManage); err != nil {
		writeJSONError(w, http.StatusForbidden, "users.manage required")
		return
	}
	if err := rbac.Enforce(ctx, p, rbac.PermSecretsWrite); err != nil {
		writeJSONError(w, http.StatusForbidden, "secrets.write required to seed a password")
		return
	}
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Username == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if s.MinPasswordLength > 0 && len(req.Password) < s.MinPasswordLength {
		writeJSONError(w, http.StatusBadRequest, "password too short")
		return
	}
	if _, err := s.Store.Users().GetByUsername(ctx, req.Username); err == nil {
		writeJSONError(w, http.StatusConflict, "username already exists")
		return
	} else if !errors.Is(err, datastore.ErrNotFound) {
		s.Log.Error("users lookup", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "create failed")
		return
	}
	hash, err := auth.HashPassword(req.Password, nil)
	if err != nil {
		s.Log.Error("hash password", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "hash failed")
		return
	}
	u := &datastore.User{
		ID:           generateUserID(req.Username),
		Username:     req.Username,
		Email:        req.Email,
		DisplayName:  req.DisplayName,
		PasswordHash: hash,
		Status:       datastore.UserActive,
	}
	if err := s.Store.Users().Create(ctx, u); err != nil {
		s.Log.Error("users create", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "create failed")
		return
	}
	// Seed built-in roles so a fresh deployment doesn't 404 on
	// "role-viewer" and friends. Cheap + idempotent.
	if _, _, err := rbac.SeedBuiltInRoles(ctx, s.Store); err != nil {
		s.Log.Warn("seed built-in roles", zap.Error(err))
	}
	for _, roleName := range req.Roles {
		if roleName = strings.TrimSpace(roleName); roleName == "" {
			continue
		}
		role, rerr := lookupRoleByIDOrName(ctx, s.Store, roleName)
		if rerr != nil {
			_ = s.appendUsersAudit(r, p, "user.create", u.ID, false, rerr)
			writeJSONError(w, http.StatusUnprocessableEntity,
				"created user but role "+roleName+" not found")
			return
		}
		if err := s.Store.Roles().AssignToUser(ctx, u.ID, role.ID); err != nil {
			s.Log.Warn("assign initial role",
				zap.String("role", roleName), zap.Error(err))
		}
	}
	_ = s.appendUsersAudit(r, p, "user.create", u.ID, true, nil)
	writeJSON(w, http.StatusCreated, userToDTOWithRoles(ctx, s.Store, u))
}

func (s *AuthService) getUser(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	if err := rbac.Enforce(ctx, p, rbac.PermUsersRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	u, err := s.Store.Users().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "user not found")
			return
		}
		s.Log.Error("users get", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, userToDTOWithRoles(ctx, s.Store, u))
}

func (s *AuthService) patchUser(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	isSelf := p != nil && p.UserID == userID
	if !isSelf {
		if err := rbac.Enforce(ctx, p, rbac.PermUsersManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "users.manage required")
			return
		}
	}
	var req patchUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	// Self-edits are restricted to non-privileged fields; a regular
	// user cannot flip their own status to "active" after an admin
	// disabled them.
	if isSelf && req.Status != nil && !rbac.Can(p, rbac.PermUsersManage) {
		writeJSONError(w, http.StatusForbidden, "cannot change own status")
		return
	}
	// Password changes require secrets.write UNLESS it is the
	// principal changing their own password.
	if req.Password != nil && !isSelf {
		if err := rbac.Enforce(ctx, p, rbac.PermSecretsWrite); err != nil {
			writeJSONError(w, http.StatusForbidden, "secrets.write required")
			return
		}
	}
	u, err := s.Store.Users().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if req.Email != nil {
		u.Email = strings.TrimSpace(*req.Email)
	}
	if req.DisplayName != nil {
		u.DisplayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.Email != nil || req.DisplayName != nil {
		if err := s.Store.Users().Update(ctx, u); err != nil {
			_ = s.appendUsersAudit(r, p, "user.update", userID, false, err)
			s.Log.Error("users update", zap.Error(err))
			writeJSONError(w, http.StatusInternalServerError, "update failed")
			return
		}
	}
	if req.Status != nil {
		status := datastore.UserStatus(strings.TrimSpace(*req.Status))
		switch status {
		case datastore.UserActive, datastore.UserDisabled, datastore.UserInvited:
			// ok
		default:
			writeJSONError(w, http.StatusBadRequest, "invalid status")
			return
		}
		// Guardrail: don't disable the last remaining owner.
		if status == datastore.UserDisabled && hasOwnerRole(ctx, s.Store, userID) {
			n, cerr := countOwnerUsers(ctx, s.Store)
			if cerr == nil && n <= 1 {
				writeJSONError(w, http.StatusConflict, "cannot disable the last owner")
				return
			}
		}
		if err := s.Store.Users().SetStatus(ctx, userID, status); err != nil {
			_ = s.appendUsersAudit(r, p, "user.status", userID, false, err)
			writeJSONError(w, http.StatusInternalServerError, "status update failed")
			return
		}
	}
	if req.Password != nil {
		pw := *req.Password
		if s.MinPasswordLength > 0 && len(pw) < s.MinPasswordLength {
			writeJSONError(w, http.StatusBadRequest, "password too short")
			return
		}
		hash, herr := auth.HashPassword(pw, nil)
		if herr != nil {
			writeJSONError(w, http.StatusInternalServerError, "hash failed")
			return
		}
		if err := s.Store.Users().SetPassword(ctx, userID, hash); err != nil {
			_ = s.appendUsersAudit(r, p, "user.password", userID, false, err)
			writeJSONError(w, http.StatusInternalServerError, "password update failed")
			return
		}
	}
	_ = s.appendUsersAudit(r, p, "user.update", userID, true, nil)
	u2, _ := s.Store.Users().Get(ctx, userID)
	if u2 == nil {
		u2 = u
	}
	writeJSON(w, http.StatusOK, userToDTOWithRoles(ctx, s.Store, u2))
}

func (s *AuthService) deleteUser(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	if err := rbac.Enforce(ctx, p, rbac.PermUsersDelete); err != nil {
		writeJSONError(w, http.StatusForbidden, "users.delete required")
		return
	}
	if p.UserID == userID {
		writeJSONError(w, http.StatusConflict, "cannot delete yourself")
		return
	}
	u, err := s.Store.Users().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	// Guardrail: never delete the last remaining owner.
	if hasOwnerRole(ctx, s.Store, userID) {
		n, ownerErr := countOwnerUsers(ctx, s.Store)
		if ownerErr == nil && n <= 1 {
			writeJSONError(w, http.StatusConflict, "cannot delete the last owner")
			return
		}
	}
	if err := s.Store.Users().Delete(ctx, u.ID); err != nil {
		_ = s.appendUsersAudit(r, p, "user.delete", userID, false, err)
		writeJSONError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	_ = s.appendUsersAudit(r, p, "user.delete", userID, true, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": userID})
}

// ─────────────────────────────────────────────────────────────────────
// user↔role
// ─────────────────────────────────────────────────────────────────────

func (s *AuthService) listUserRoles(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	if err := rbac.Enforce(ctx, p, rbac.PermUsersRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	roles, err := s.Store.Roles().ListRolesForUser(ctx, userID)
	if err != nil {
		s.Log.Error("user roles list", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	out := make([]roleDTO, 0, len(roles))
	for i := range roles {
		out = append(out, rolesRowToDTO(&roles[i], nil))
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": out})
}

func (s *AuthService) grantUserRole(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	if err := rbac.Enforce(ctx, p, rbac.PermRolesManage); err != nil {
		writeJSONError(w, http.StatusForbidden, "roles.manage required")
		return
	}
	var req grantRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Role = strings.TrimSpace(req.Role)
	if req.Role == "" {
		writeJSONError(w, http.StatusBadRequest, "role is required")
		return
	}
	if _, err := s.Store.Users().Get(ctx, userID); err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	role, err := lookupRoleByIDOrName(ctx, s.Store, req.Role)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "role not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "role lookup failed")
		return
	}
	if err := s.Store.Roles().AssignToUser(ctx, userID, role.ID); err != nil {
		_ = s.appendUsersAudit(r, p, "user.role.grant", userID, false, err)
		writeJSONError(w, http.StatusInternalServerError, "assign failed")
		return
	}
	_ = s.appendUsersAuditMeta(r, p, "user.role.grant", userID, true, nil, map[string]any{
		"role_id":   role.ID,
		"role_name": role.Name,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"role":   rolesRowToDTO(role, nil),
	})
}

func (s *AuthService) revokeUserRole(w http.ResponseWriter, r *http.Request, userID, roleIDOrName string) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	if err := rbac.Enforce(ctx, p, rbac.PermRolesManage); err != nil {
		writeJSONError(w, http.StatusForbidden, "roles.manage required")
		return
	}
	role, err := lookupRoleByIDOrName(ctx, s.Store, roleIDOrName)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "role not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "role lookup failed")
		return
	}
	// Guardrail: never revoke the last remaining owner.
	if role.ID == "role-owner" && hasOwnerRole(ctx, s.Store, userID) {
		n, ownerErr := countOwnerUsers(ctx, s.Store)
		if ownerErr == nil && n <= 1 {
			writeJSONError(w, http.StatusConflict, "cannot revoke role-owner from the last owner")
			return
		}
	}
	if err := s.Store.Roles().RevokeFromUser(ctx, userID, role.ID); err != nil {
		_ = s.appendUsersAudit(r, p, "user.role.revoke", userID, false, err)
		writeJSONError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	_ = s.appendUsersAuditMeta(r, p, "user.role.revoke", userID, true, nil, map[string]any{
		"role_id":   role.ID,
		"role_name": role.Name,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ─────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────

func userToDTO(u *datastore.User) userDTO {
	if u == nil {
		return userDTO{}
	}
	return userDTO{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Status:      string(u.Status),
		OIDCSubject: u.OIDCSubject,
		OIDCIssuer:  u.OIDCIssuer,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
		LastLoginAt: u.LastLoginAt,
	}
}

func userToDTOWithRoles(ctx context.Context, store datastore.Store, u *datastore.User) userDTO {
	dto := userToDTO(u)
	if roles, err := store.Roles().ListRolesForUser(ctx, u.ID); err == nil {
		dto.Roles = roleNames(roles)
	}
	return dto
}

func rolesRowToDTO(r *datastore.Role, perms []datastore.Permission) roleDTO {
	if r == nil {
		return roleDTO{}
	}
	dto := roleDTO{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		IsBuiltIn:   r.IsBuiltIn,
	}
	if len(perms) > 0 {
		dto.Permissions = make([]string, 0, len(perms))
		for _, p := range perms {
			dto.Permissions = append(dto.Permissions, string(p))
		}
	}
	return dto
}

func roleNames(roles []datastore.Role) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, r.Name)
	}
	return out
}

// lookupRoleByIDOrName accepts "role-owner" (ID), "owner" (name), or
// any custom role's name. Returns datastore.ErrNotFound if neither
// matches.
func lookupRoleByIDOrName(ctx context.Context, store datastore.Store, idOrName string) (*datastore.Role, error) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return nil, datastore.ErrNotFound
	}
	if r, err := store.Roles().Get(ctx, idOrName); err == nil {
		return r, nil
	}
	if r, err := store.Roles().GetByName(ctx, idOrName); err == nil {
		return r, nil
	}
	// Fallback: tolerate "<name>" → "role-<name>" for short names.
	if !strings.HasPrefix(idOrName, "role-") {
		if r, err := store.Roles().Get(ctx, "role-"+idOrName); err == nil {
			return r, nil
		}
	}
	return nil, datastore.ErrNotFound
}

func hasOwnerRole(ctx context.Context, store datastore.Store, userID string) bool {
	roles, err := store.Roles().ListRolesForUser(ctx, userID)
	if err != nil {
		return false
	}
	for _, r := range roles {
		if r.ID == "role-owner" {
			return true
		}
	}
	return false
}

func countOwnerUsers(ctx context.Context, store datastore.Store) (int, error) {
	users, err := store.Users().List(ctx, 1000, 0)
	if err != nil {
		return 0, err
	}
	count := 0
	for i := range users {
		if hasOwnerRole(ctx, store, users[i].ID) {
			count++
		}
	}
	return count, nil
}

// generateUserID mirrors cmd/opsintelligence admin user add's shape so
// API-created and CLI-created rows look indistinguishable.
func generateUserID(username string) string {
	clean := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(username), " ", "-"))
	tok, err := auth.RandomToken(6)
	if err != nil {
		return "user-" + clean
	}
	return "user-" + clean + "-" + tok[:8]
}

func (s *AuthService) appendUsersAudit(r *http.Request, p *auth.Principal, action, userID string, success bool, err error) error {
	return s.appendUsersAuditMeta(r, p, action, userID, success, err, nil)
}

func (s *AuthService) appendUsersAuditMeta(r *http.Request, p *auth.Principal, action, userID string, success bool, err error, extra map[string]any) error {
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
		ResourceType: "user",
		ResourceID:   userID,
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
