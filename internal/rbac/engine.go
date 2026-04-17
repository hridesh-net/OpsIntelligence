package rbac

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/auth"
)

// ErrDenied is the sentinel returned by Enforce when the principal
// lacks the requested permission. Upstream HTTP handlers translate it
// to 403 Forbidden; the guardrail translates it to a Block.
var ErrDenied = errors.New("rbac: permission denied")

// ErrNotAuthenticated is returned when Enforce is called with an
// anonymous principal but a permission is required. Callers that want
// to short-circuit to "log in first" should check errors.Is against
// this sentinel first.
var ErrNotAuthenticated = errors.New("rbac: not authenticated")

// DeniedError is the rich variant of ErrDenied that carries the
// denied permission + principal for logging. errors.Is(err, ErrDenied)
// still succeeds on it.
type DeniedError struct {
	Principal  *auth.Principal
	Permission Permission
}

func (e *DeniedError) Error() string {
	who := "anonymous"
	if e.Principal != nil && e.Principal.Username != "" {
		who = fmt.Sprintf("%s(%s)", e.Principal.Username, e.Principal.Type)
	}
	return fmt.Sprintf("rbac: permission %q denied for %s", e.Permission, who)
}

// Is implements errors.Is so `errors.Is(err, ErrDenied)` succeeds.
func (e *DeniedError) Is(target error) bool { return target == ErrDenied }

// ─────────────────────────────────────────────────────────────────────
// hot-path enforcement
// ─────────────────────────────────────────────────────────────────────

// Enforce returns nil if the principal holds the requested permission,
// a DeniedError otherwise. System principals always succeed; anonymous
// principals always fail with ErrNotAuthenticated wrapped in
// DeniedError.
//
// Enforce is pure: no datastore access, no logging. Callers are
// expected to emit their own audit entry on ErrDenied — see
// internal/gateway middleware.
func Enforce(ctx context.Context, p *auth.Principal, perm Permission) error {
	if p == nil {
		p = auth.AnonymousPrincipal
	}
	if p.IsSystem() {
		return nil
	}
	if !p.IsAuthenticated() {
		return fmt.Errorf("%w: %s", ErrNotAuthenticated, perm)
	}
	if Can(p, perm) {
		return nil
	}
	return &DeniedError{Principal: p, Permission: perm}
}

// EnforceAny succeeds when the principal holds at least one of the
// listed permissions. Useful for endpoints accessible to both the
// owner (apikeys.manage.all) and the end user (apikeys.manage.own).
func EnforceAny(ctx context.Context, p *auth.Principal, perms ...Permission) error {
	if len(perms) == 0 {
		return nil
	}
	if p == nil {
		p = auth.AnonymousPrincipal
	}
	if p.IsSystem() {
		return nil
	}
	if !p.IsAuthenticated() {
		return fmt.Errorf("%w: %v", ErrNotAuthenticated, perms)
	}
	for _, perm := range perms {
		if Can(p, perm) {
			return nil
		}
	}
	return &DeniedError{Principal: p, Permission: perms[0]}
}

// EnforceAll succeeds only when the principal holds every listed
// permission. Rare — most handlers only care about "any".
func EnforceAll(ctx context.Context, p *auth.Principal, perms ...Permission) error {
	for _, perm := range perms {
		if err := Enforce(ctx, p, perm); err != nil {
			return err
		}
	}
	return nil
}

// Can is the pure, alloc-free permission check. Returns true if the
// principal's granted permissions satisfy perm. System principals are
// always true; nil / anonymous always false.
//
// Exposed separately from Enforce so handlers that render different
// UI based on "can I?" don't have to allocate DeniedError instances.
func Can(p *auth.Principal, perm Permission) bool {
	if p == nil || !p.IsAuthenticated() {
		return false
	}
	if p.IsSystem() {
		return true
	}
	for _, granted := range p.Permissions {
		if perm.Matches(granted) {
			return true
		}
	}
	return false
}

// CanAny is Can for any-of semantics.
func CanAny(p *auth.Principal, perms ...Permission) bool {
	for _, perm := range perms {
		if Can(p, perm) {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────
// credential-load-time helpers
// ─────────────────────────────────────────────────────────────────────

// FlattenPermissions deduplicates and sorts a set of granted permission
// strings. Callers use this at login time so the hot-path Can() stays
// a simple range-loop over a small stable slice.
//
// Wildcard entries ("tasks.*", "*") are preserved verbatim — the
// matcher does not need them "expanded". Empty strings are dropped.
func FlattenPermissions(granted []string) []string {
	if len(granted) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(granted))
	out := make([]string, 0, len(granted))
	for _, g := range granted {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if _, ok := seen[g]; ok {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}
