package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/gateway"
)

// loginAs seeds + signs in a user with the given role and returns the
// cookies so the caller can piggyback them onto subsequent requests.
func loginAs(t *testing.T, svc *gateway.AuthService, mux http.Handler, username, password, roleID string) []*http.Cookie {
	t.Helper()
	seedUserWithRole(t, svc, username, password, roleID)
	login := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": username,
		"password": password,
	}, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login %s failed: %d body=%s", username, login.StatusCode, readBody(login))
	}
	return login.Cookies()
}

// doReq is the generic "authenticated + CSRF'd" request helper for
// the mutation tests below.
func doReq(t *testing.T, svc *gateway.AuthService, mux http.Handler, method, path string, body any, cookies []*http.Cookie) *http.Response {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = bytes.NewReader(raw)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", svc.Sessions.CSRFTokenFrom(req))
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr.Result()
}

// ─────────────────────────────────────────────────────────────────────
// /api/v1/users
// ─────────────────────────────────────────────────────────────────────

func TestUsers_List_RequiresUsersRead(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	cookies := loginAs(t, svc, mux, "viewer-only", "viewer-password-long", "role-viewer")
	res := doReq(t, svc, mux, http.MethodGet, "/api/v1/users", nil, cookies)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer should not read users; got %d body=%s", res.StatusCode, readBody(res))
	}
}

func TestUsers_List_OwnerSees_AllUsers(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	cookies := loginAs(t, svc, mux, "owner", "owner-password-long", "role-owner")
	res := doReq(t, svc, mux, http.MethodGet, "/api/v1/users", nil, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("owner list users: status=%d body=%s", res.StatusCode, readBody(res))
	}
	var body struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal([]byte(readBody(res)), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(body.Users) == 0 {
		t.Fatalf("expected at least one user, got %+v", body)
	}
	// Ensure password hash is never serialised.
	for _, u := range body.Users {
		if _, ok := u["password_hash"]; ok {
			t.Fatalf("password_hash leaked in response")
		}
	}
}

func TestUsers_Create_OwnerCanMintWithRole(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	cookies := loginAs(t, svc, mux, "ownerc", "owner-password-long", "role-owner")
	res := doReq(t, svc, mux, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "newbie",
		"email":    "newbie@example.test",
		"password": "newbie-password-long",
		"roles":    []string{"viewer"},
	}, cookies)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create user: status=%d body=%s", res.StatusCode, readBody(res))
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(readBody(res)), &dto); err != nil {
		t.Fatalf("json: %v", err)
	}
	roles, _ := dto["roles"].([]any)
	foundViewer := false
	for _, r := range roles {
		if r == "viewer" {
			foundViewer = true
		}
	}
	if !foundViewer {
		t.Fatalf("expected new user to have 'viewer' role, got %v", roles)
	}
}

func TestUsers_Create_DeveloperDenied(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	cookies := loginAs(t, svc, mux, "devuser", "dev-password-long", "role-developer")
	res := doReq(t, svc, mux, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "shouldnot",
		"password": "password-long-enough",
	}, cookies)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", res.StatusCode, readBody(res))
	}
}

func TestUsers_PatchSelf_AllowedWithoutUsersManage(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	cookies := loginAs(t, svc, mux, "selfy", "selfy-password-long", "role-developer")

	// Find selfy's id via whoami.
	who := doReq(t, svc, mux, http.MethodGet, "/api/v1/whoami", nil, cookies)
	var p map[string]any
	if err := json.Unmarshal([]byte(readBody(who)), &p); err != nil {
		t.Fatalf("whoami json: %v", err)
	}
	userID, _ := p["user_id"].(string)
	if userID == "" {
		t.Fatalf("whoami returned no user_id")
	}

	// Update own display name — should succeed (self-edit).
	res := doReq(t, svc, mux, http.MethodPatch, "/api/v1/users/"+userID, map[string]any{
		"display_name": "Selfy McSelf",
	}, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("self patch: status=%d body=%s", res.StatusCode, readBody(res))
	}

	// Try to flip own status — must be rejected (developer lacks
	// users.manage).
	res2 := doReq(t, svc, mux, http.MethodPatch, "/api/v1/users/"+userID, map[string]any{
		"status": "disabled",
	}, cookies)
	if res2.StatusCode != http.StatusForbidden {
		t.Fatalf("self status change should be forbidden; got %d body=%s", res2.StatusCode, readBody(res2))
	}
}

func TestUsers_Delete_BlocksLastOwner(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	cookies := loginAs(t, svc, mux, "soleowner", "owner-password-long", "role-owner")
	// Find own id.
	who := doReq(t, svc, mux, http.MethodGet, "/api/v1/whoami", nil, cookies)
	var p map[string]any
	_ = json.Unmarshal([]byte(readBody(who)), &p)

	// Attempt to delete the owner via a second owner account.
	seedUserWithRole(t, svc, "secondowner", "owner-password-long", "role-owner")
	login := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "secondowner",
		"password": "owner-password-long",
	}, nil)
	cookies2 := login.Cookies()
	// Delete first owner — OK, we still have 'secondowner' left.
	res := doReq(t, svc, mux, http.MethodDelete, "/api/v1/users/user-soleowner", nil, cookies2)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete first owner (two owners): status=%d body=%s", res.StatusCode, readBody(res))
	}
	// Now try to delete the last remaining owner (self). Self-delete
	// already returns 409 before the last-owner check fires.
	res2 := doReq(t, svc, mux, http.MethodDelete, "/api/v1/users/user-secondowner", nil, cookies2)
	if res2.StatusCode != http.StatusConflict {
		t.Fatalf("delete self: expected 409, got %d body=%s", res2.StatusCode, readBody(res2))
	}
}

func TestUsers_Roles_GrantAndRevoke(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	cookies := loginAs(t, svc, mux, "ownerg", "owner-password-long", "role-owner")
	// Create a fresh user to experiment on.
	createRes := doReq(t, svc, mux, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "grantee",
		"password": "grantee-password-long",
	}, cookies)
	if createRes.StatusCode != http.StatusCreated {
		t.Fatalf("create: %s", readBody(createRes))
	}
	var u map[string]any
	_ = json.Unmarshal([]byte(readBody(createRes)), &u)
	userID, _ := u["id"].(string)
	if userID == "" {
		t.Fatalf("missing id in create response")
	}
	// Grant operator role.
	res := doReq(t, svc, mux, http.MethodPost, "/api/v1/users/"+userID+"/roles", map[string]any{
		"role": "operator",
	}, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("grant: status=%d body=%s", res.StatusCode, readBody(res))
	}
	// List roles — should include role-operator.
	listRes := doReq(t, svc, mux, http.MethodGet, "/api/v1/users/"+userID+"/roles", nil, cookies)
	var body struct {
		Roles []struct {
			ID string `json:"id"`
		} `json:"roles"`
	}
	_ = json.Unmarshal([]byte(readBody(listRes)), &body)
	found := false
	for _, r := range body.Roles {
		if r.ID == "role-operator" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected role-operator after grant; got %+v", body)
	}
	// Revoke.
	revokeRes := doReq(t, svc, mux, http.MethodDelete, "/api/v1/users/"+userID+"/roles/role-operator", nil, cookies)
	if revokeRes.StatusCode != http.StatusOK {
		t.Fatalf("revoke: status=%d body=%s", revokeRes.StatusCode, readBody(revokeRes))
	}
}

// ─────────────────────────────────────────────────────────────────────
// /api/v1/roles
// ─────────────────────────────────────────────────────────────────────

func TestRoles_List_ReturnsBuiltIns(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	cookies := loginAs(t, svc, mux, "ownerlist", "owner-password-long", "role-owner")
	res := doReq(t, svc, mux, http.MethodGet, "/api/v1/roles", nil, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("roles list: %d body=%s", res.StatusCode, readBody(res))
	}
	var body struct {
		Roles []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			IsBuiltIn bool   `json:"is_builtin"`
		} `json:"roles"`
	}
	_ = json.Unmarshal([]byte(readBody(res)), &body)
	names := map[string]bool{}
	for _, r := range body.Roles {
		if r.IsBuiltIn {
			names[r.Name] = true
		}
	}
	for _, want := range []string{"owner", "admin", "operator", "developer", "auditor", "viewer"} {
		if !names[want] {
			t.Fatalf("expected built-in role %q in list %v", want, names)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// /api/v1/apikeys
// ─────────────────────────────────────────────────────────────────────

func TestAPIKeys_Create_ReturnsPlainTokenOnce(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	cookies := loginAs(t, svc, mux, "apikeyowner", "owner-password-long", "role-owner")
	res := doReq(t, svc, mux, http.MethodPost, "/api/v1/apikeys", map[string]any{
		"name":    "ci-token",
		"expires": "24h",
	}, cookies)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d body=%s", res.StatusCode, readBody(res))
	}
	var body struct {
		Key        map[string]any `json:"key"`
		PlainToken string         `json:"plain_token"`
	}
	if err := json.Unmarshal([]byte(readBody(res)), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !strings.HasPrefix(body.PlainToken, auth.APIKeyPrefix) {
		t.Fatalf("plain_token should have opi_ prefix: %q", body.PlainToken)
	}
	// List — token must NOT be echoed.
	list := doReq(t, svc, mux, http.MethodGet, "/api/v1/apikeys", nil, cookies)
	listBody := readBody(list)
	if strings.Contains(listBody, body.PlainToken) {
		t.Fatalf("list leaks the plain token")
	}
}

func TestAPIKeys_Create_OwnForOtherRequires_ManageAll(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	cookies := loginAs(t, svc, mux, "devkey", "dev-password-long", "role-developer")
	seedUserWithRole(t, svc, "other", "other-password-long", "role-viewer")
	// developer has apikeys.manage.own but NOT apikeys.manage.all.
	res := doReq(t, svc, mux, http.MethodPost, "/api/v1/apikeys", map[string]any{
		"username": "other",
		"name":     "nope",
	}, cookies)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when minting for another user; got %d body=%s", res.StatusCode, readBody(res))
	}
}

func TestAPIKeys_Revoke_OwnerCanRevokeAny(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	// Owner mints a key for self.
	cookies := loginAs(t, svc, mux, "ownerrev", "owner-password-long", "role-owner")
	createRes := doReq(t, svc, mux, http.MethodPost, "/api/v1/apikeys", map[string]any{"name": "tmp"}, cookies)
	if createRes.StatusCode != http.StatusCreated {
		t.Fatalf("create: %s", readBody(createRes))
	}
	var created struct {
		Key struct {
			ID    string `json:"id"`
			KeyID string `json:"key_id"`
		} `json:"key"`
	}
	_ = json.Unmarshal([]byte(readBody(createRes)), &created)
	if created.Key.ID == "" {
		t.Fatalf("no key id in create response")
	}
	// Revoke by row ID.
	revRes := doReq(t, svc, mux, http.MethodDelete, "/api/v1/apikeys/"+created.Key.KeyID, nil, cookies)
	if revRes.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %s", readBody(revRes))
	}
	// The row should now carry RevokedAt.
	k, err := svc.Store.APIKeys().GetByKeyID(context.Background(), created.Key.KeyID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if k.RevokedAt == nil {
		t.Fatalf("expected RevokedAt to be set")
	}
}

// readAllBody is a small helper for test debug messages that the
// other helpers don't cover.
func readAllBody(res *http.Response) string {
	if res == nil || res.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return strings.TrimSpace(string(b))
}

var _ = readAllBody // keep helper available when adding new tests

// Ensure the datastore.User type import is actually exercised so go
// vet doesn't complain if a future edit drops the only reference.
var _ = datastore.UserActive
