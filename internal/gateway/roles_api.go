package gateway

import (
	"errors"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// roles_api.go exposes the role catalogue as read-only for phase 3d.
// Custom-role CRUD will land in a follow-up once the UI needs it —
// today the dashboard only needs to render the built-in role picker
// when granting/revoking from users.
//
//	GET /api/v1/roles        — all roles, with their permissions
//	GET /api/v1/roles/{id}   — single role + permissions

func (s *AuthService) handleRoles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	if err := rbac.Enforce(ctx, p, rbac.PermRolesRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	// Re-seed so a fresh deployment without a CLI-driven `admin init`
	// still populates the picker.
	if _, _, err := rbac.SeedBuiltInRoles(ctx, s.Store); err != nil {
		s.Log.Warn("seed built-in roles", zap.Error(err))
	}
	roles, err := s.Store.Roles().List(ctx)
	if err != nil {
		s.Log.Error("roles list", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "roles list failed")
		return
	}
	out := make([]roleDTO, 0, len(roles))
	for i := range roles {
		role := &roles[i]
		perms, _ := s.Store.Roles().ListPermissions(ctx, role.ID)
		out = append(out, rolesRowToDTO(role, perms))
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": out})
}

func (s *AuthService) handleRoleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/roles/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		writeJSONError(w, http.StatusNotFound, "role id required")
		return
	}
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	if err := rbac.Enforce(ctx, p, rbac.PermRolesRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	role, err := lookupRoleByIDOrName(ctx, s.Store, rest)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "role not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	perms, _ := s.Store.Roles().ListPermissions(ctx, role.ID)
	writeJSON(w, http.StatusOK, rolesRowToDTO(role, perms))
}
