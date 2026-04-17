package rbac

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// SeedBuiltInRoles upserts every role returned by BuiltInRoles into
// the datastore and replaces their permission grants with the current
// in-code definition.
//
// Safe to call on every boot — existing custom roles are untouched;
// the built-in roles always reflect the shipped constants. Returns
// (created, updated) counts for logging.
func SeedBuiltInRoles(ctx context.Context, store datastore.Store) (created, updated int, err error) {
	if store == nil {
		return 0, 0, errors.New("rbac: SeedBuiltInRoles requires a non-nil datastore.Store")
	}
	rolesRepo := store.Roles()
	for _, spec := range BuiltInRoles() {
		existing, getErr := rolesRepo.Get(ctx, spec.ID)
		switch {
		case errors.Is(getErr, datastore.ErrNotFound):
			if err := rolesRepo.Create(ctx, &datastore.Role{
				ID:          spec.ID,
				Name:        spec.Name,
				Description: spec.Description,
				IsBuiltIn:   true,
			}); err != nil {
				return created, updated, fmt.Errorf("rbac: create %s: %w", spec.Name, err)
			}
			created++
		case getErr != nil:
			return created, updated, fmt.Errorf("rbac: lookup %s: %w", spec.ID, getErr)
		default:
			// Keep the ID/Name stable; refresh description so doc
			// edits take effect on next boot.
			if existing.Description != spec.Description || existing.Name != spec.Name {
				existing.Description = spec.Description
				existing.Name = spec.Name
				if err := rolesRepo.Update(ctx, existing); err != nil {
					return created, updated, fmt.Errorf("rbac: update %s: %w", spec.Name, err)
				}
			}
			updated++
		}
		perms := make([]datastore.Permission, 0, len(spec.Permissions))
		for _, p := range spec.Permissions {
			perms = append(perms, datastore.Permission(p))
		}
		if err := rolesRepo.SetPermissions(ctx, spec.ID, perms); err != nil {
			return created, updated, fmt.Errorf("rbac: set perms %s: %w", spec.Name, err)
		}
	}
	return created, updated, nil
}

// BootstrapOwner is the first-run helper: if the datastore has zero
// users, it creates `owner` with the given hashed password and grants
// role-owner. If any user already exists it returns (nil, nil, nil) —
// caller decides whether to treat that as a fatal "already initialised"
// (the CLI does) or a no-op (the gateway does on every boot).
//
// The caller is responsible for hashing passwordHash beforehand (see
// internal/auth/passwords.go once phase 2b lands); this package does
// not import hashing libraries to stay dep-light.
func BootstrapOwner(ctx context.Context, store datastore.Store, username, email, passwordHash string) (*datastore.User, bool, error) {
	if store == nil {
		return nil, false, errors.New("rbac: BootstrapOwner requires a non-nil datastore.Store")
	}
	n, err := store.Users().Count(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("rbac: count users: %w", err)
	}
	if n > 0 {
		return nil, false, nil
	}
	if _, _, err := SeedBuiltInRoles(ctx, store); err != nil {
		return nil, false, err
	}
	u := &datastore.User{
		ID:           "user-owner",
		Username:     username,
		Email:        email,
		PasswordHash: passwordHash,
		Status:       datastore.UserActive,
	}
	if err := store.Users().Create(ctx, u); err != nil {
		return nil, false, fmt.Errorf("rbac: create owner: %w", err)
	}
	if err := store.Roles().AssignToUser(ctx, u.ID, "role-owner"); err != nil {
		return nil, false, fmt.Errorf("rbac: grant owner: %w", err)
	}
	return u, true, nil
}

// ─────────────────────────────────────────────────────────────────────
// credential → principal resolution
// ─────────────────────────────────────────────────────────────────────

// Resolver loads roles+permissions for a given datastore user and
// builds an auth.Principal. The Authenticator middleware (phase 2b)
// calls it once per successful credential check and passes the result
// down the chain.
type Resolver struct {
	Store datastore.Store
}

// NewResolver constructs a Resolver backed by store.
func NewResolver(store datastore.Store) *Resolver { return &Resolver{Store: store} }

// ForUser loads roles + flattened permissions for userID and returns a
// PrincipalUser ready to attach to request context.
func (r *Resolver) ForUser(ctx context.Context, user *datastore.User) (*auth.Principal, error) {
	if r == nil || r.Store == nil {
		return nil, errors.New("rbac: resolver not initialised")
	}
	if user == nil {
		return nil, errors.New("rbac: ForUser requires a non-nil user")
	}
	roles, err := r.Store.Roles().ListRolesForUser(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("rbac: list roles: %w", err)
	}
	perms, err := r.Store.Roles().ListPermissionsForUser(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("rbac: list perms: %w", err)
	}
	roleNames := make([]string, 0, len(roles))
	for _, role := range roles {
		roleNames = append(roleNames, role.Name)
	}
	sort.Strings(roleNames)

	permStrs := make([]string, 0, len(perms))
	for _, p := range perms {
		permStrs = append(permStrs, string(p))
	}
	return &auth.Principal{
		Type:        auth.PrincipalUser,
		UserID:      user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Roles:       roleNames,
		Permissions: FlattenPermissions(permStrs),
	}, nil
}

// ForAPIKey builds a PrincipalAPIKey principal. The API key's scopes
// (if any) are applied as an intersection with the owning user's
// permissions — a key cannot elevate beyond its owner, and can opt to
// restrict itself further. An empty scope list inherits the full user
// permission set.
func (r *Resolver) ForAPIKey(ctx context.Context, key *datastore.APIKey) (*auth.Principal, error) {
	if r == nil || r.Store == nil {
		return nil, errors.New("rbac: resolver not initialised")
	}
	if key == nil {
		return nil, errors.New("rbac: ForAPIKey requires a non-nil key")
	}
	user, err := r.Store.Users().Get(ctx, key.UserID)
	if err != nil {
		return nil, fmt.Errorf("rbac: load key owner: %w", err)
	}
	ownerPrincipal, err := r.ForUser(ctx, user)
	if err != nil {
		return nil, err
	}
	principal := *ownerPrincipal // copy by value
	principal.Type = auth.PrincipalAPIKey
	principal.Username = user.Username + "/" + key.KeyID
	principal.APIKeyID = key.KeyID

	if len(key.Scopes) > 0 {
		allowed := make(map[string]struct{}, len(key.Scopes))
		for _, s := range key.Scopes {
			allowed[s] = struct{}{}
		}
		kept := make([]string, 0, len(ownerPrincipal.Permissions))
		for _, p := range ownerPrincipal.Permissions {
			if _, ok := allowed[p]; ok {
				kept = append(kept, p)
			}
		}
		principal.Permissions = kept
	}
	return &principal, nil
}
