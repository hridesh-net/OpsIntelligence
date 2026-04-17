# Users, Roles & API Keys API (Phase 3d)

OpsIntelligence exposes user, role, and API-key management over the
gateway's `/api/v1/*` surface so the dashboard can do everything the
`opsintelligence admin` CLI already does — with the same RBAC, the
same guardrails, and the same audit log.

All endpoints in this document run behind the `Authenticator`
middleware (cookie session **or** `Authorization: Bearer <apikey>`).
Mutating verbs additionally require CSRF on cookie sessions
(`X-CSRF-Token` header paired with the `opi_csrf` cookie) via
`ProtectCSRF`. API-key callers are exempt from CSRF by virtue of the
per-scheme logic in `Authenticator`.

Every mutation writes a row to the audit log (`audit_log` table) via
`internal/datastore.AuditRepo.Append`. Metadata includes the HTTP
path, method, and — for role/key operations — the resolved role/key
IDs.

## Contents

- [Permissions reference](#permissions-reference)
- [Users](#users)
  - [`GET /api/v1/users`](#get-apiv1users)
  - [`POST /api/v1/users`](#post-apiv1users)
  - [`GET /api/v1/users/{id}`](#get-apiv1usersid)
  - [`PATCH /api/v1/users/{id}`](#patch-apiv1usersid)
  - [`DELETE /api/v1/users/{id}`](#delete-apiv1usersid)
- [User roles](#user-roles)
  - [`GET /api/v1/users/{id}/roles`](#get-apiv1usersidroles)
  - [`POST /api/v1/users/{id}/roles`](#post-apiv1usersidroles)
  - [`DELETE /api/v1/users/{id}/roles/{roleIDOrName}`](#delete-apiv1usersidrolesroleidorname)
- [Roles (read-only)](#roles-read-only)
  - [`GET /api/v1/roles`](#get-apiv1roles)
  - [`GET /api/v1/roles/{idOrName}`](#get-apiv1rolesidorname)
- [API keys](#api-keys)
  - [`GET /api/v1/apikeys`](#get-apiv1apikeys)
  - [`POST /api/v1/apikeys`](#post-apiv1apikeys)
  - [`DELETE /api/v1/apikeys/{id}`](#delete-apiv1apikeysid)
- [Guardrails](#guardrails)
- [Audit events](#audit-events)

## Permissions reference

Declared in `internal/rbac/permissions.go` — wildcards (`users.*`,
`apikeys.*`, `*`) match as expected.

| Permission            | Used by                                                                           |
| --------------------- | --------------------------------------------------------------------------------- |
| `users.read`          | list users, get user, list user roles                                             |
| `users.manage`        | create / patch users (non-self), change another user's status                     |
| `users.delete`        | delete a user                                                                     |
| `secrets.write`       | seed a password on user creation, reset **another** user's password               |
| `roles.read`          | list roles, get role                                                              |
| `roles.manage`        | grant / revoke roles on a user                                                    |
| `apikeys.read.own`    | list **own** API keys                                                             |
| `apikeys.read.all`    | list **all** API keys                                                             |
| `apikeys.manage.own`  | create / revoke **own** API keys                                                  |
| `apikeys.manage.all`  | create keys for other users, revoke any key                                       |

The built-in roles (`owner`, `admin`, `operator`, `developer`,
`auditor`, `viewer`) are seeded lazily by
`rbac.SeedBuiltInRoles` — the user-create handler calls this so a
fresh deployment never 404s on `role-viewer`.

## Users

### `GET /api/v1/users`

Lists up to 500 users with their assigned role names.

**Required:** `users.read`

```json
{
  "users": [
    {
      "id": "user-alice-ab12cd34",
      "username": "alice",
      "email": "alice@example.com",
      "display_name": "Alice",
      "status": "active",
      "created_at": "2026-04-01T10:00:00Z",
      "updated_at": "2026-04-16T09:10:00Z",
      "last_login_at": "2026-04-16T09:10:00Z",
      "roles": ["owner"]
    }
  ]
}
```

Password hashes are never serialised.

### `POST /api/v1/users`

Creates a local user, hashes the password with argon2id, and
optionally assigns initial roles.

**Required:** `users.manage` **and** `secrets.write`

Request:

```json
{
  "username": "bob",
  "email": "bob@example.com",
  "display_name": "Bob",
  "password": "correct horse battery staple",
  "roles": ["developer"]
}
```

Role entries accept **role IDs** (`role-owner`), **role names**
(`owner`), or short names (`viewer` → `role-viewer`).

Errors:

- `400 invalid json` / `400 username and password are required` /
  `400 password too short` (when `auth.min_password_length` is set)
- `409 username already exists`
- `422 created user but role <x> not found` — row is persisted, the
  payload indicates which role failed to resolve.

Success (`201 Created`) returns the `userDTO` with the `roles` field
populated.

### `GET /api/v1/users/{id}`

Returns one user plus assigned role names. `users.read`.

### `PATCH /api/v1/users/{id}`

Partial update. Only fields present in the body change. `nil`/absent
fields are left untouched.

```json
{
  "email": "new@example.com",
  "display_name": "Alice A.",
  "status": "disabled",
  "password": "<new>"
}
```

**Permission matrix:**

| Target       | Requirement                                                                 |
| ------------ | --------------------------------------------------------------------------- |
| self         | authenticated; **not** allowed to change own `status` unless `users.manage` |
| another user | `users.manage`; `secrets.write` additionally required to change `password`  |

Valid statuses: `active`, `disabled`, `invited`.

Guardrails:

- `409 cannot disable the last owner` — the last remaining user with
  `role-owner` can never be set to `disabled`.

### `DELETE /api/v1/users/{id}`

Hard-deletes a user. **Required:** `users.delete`.

Guardrails:

- `409 cannot delete yourself` — you can't delete the principal
  making the request.
- `409 cannot delete the last owner`.

## User roles

### `GET /api/v1/users/{id}/roles`

Lists the roles assigned to a user. `users.read`.

### `POST /api/v1/users/{id}/roles`

Grants a role. `roles.manage`.

```json
{ "role": "operator" }
```

Role resolution is identical to `POST /api/v1/users` (`role-owner`,
`owner`, or short names).

### `DELETE /api/v1/users/{id}/roles/{roleIDOrName}`

Revokes a role. `roles.manage`.

Guardrails:

- `409 cannot revoke role-owner from the last owner`.

## Roles (read-only)

Custom role CRUD is intentionally out-of-scope for Phase 3d — the
built-in roles already cover the permissions matrix the dashboard
needs, and opening up role creation without a UI for the permission
catalogue would be a footgun. These endpoints exist so the dashboard
can render the "assign role" picker and display role descriptions.

### `GET /api/v1/roles`

Lists all roles (built-in first). `roles.read`. Seeds the built-in
roles on first call.

```json
{
  "roles": [
    {
      "id": "role-owner",
      "name": "owner",
      "description": "Full administrative access",
      "is_builtin": true,
      "permissions": ["*"]
    }
  ]
}
```

### `GET /api/v1/roles/{idOrName}`

Returns one role plus its permission list. `roles.read`.

## API keys

Plaintext tokens have shape `opi_<key-id>_<secret>`. They are hashed
with argon2id before persistence; `GET` and `POST` list responses
**never** include the plaintext. `POST` is the **only** place the
plaintext is returned — one-shot on mint.

The dashboard renders this in a modal that forces the operator to
copy it before dismissal; the "Users & API Keys" README section
reinforces this.

### `GET /api/v1/apikeys`

Lists API keys. Behaviour depends on permissions and the optional
`?mine=1` query parameter:

| Caller permission(s)                | `?mine=1` | Result         |
| ----------------------------------- | --------- | -------------- |
| `apikeys.read.all` (no `?mine=1`)   | no        | all keys       |
| `apikeys.read.all` + `?mine=1`      | yes       | only own keys  |
| `apikeys.read.own` only             | —         | only own keys  |
| neither                             | —         | `403 Forbidden`|

Owner usernames are looked up and embedded in each row for UX, but
the row still carries `user_id` for scripting.

Status values: `active`, `revoked`, `expired` (computed from
`expires_at`).

### `POST /api/v1/apikeys`

Mints a new API key.

**Permission matrix:**

| Target owner | Requirement                                                   |
| ------------ | ------------------------------------------------------------- |
| self         | `apikeys.manage.own` **or** `apikeys.manage.all`              |
| another user | `apikeys.manage.all`                                          |

Additionally, `auth.api_keys.enabled` must be `true` in config —
otherwise the endpoint returns `403 api keys are disabled in config`
regardless of RBAC.

Request:

```json
{
  "user_id": "user-bob-...",   // OR "username": "bob" — omit both to mint for self
  "name": "ci-runner",
  "expires": "720h",           // Go duration; "" = no expiry
  "scopes": ["tasks.read"]
}
```

Owner is resolved in this order: `user_id` → `username` → the
calling principal. The owner must be `active`.

Response (`201 Created`):

```json
{
  "key": {
    "id": "ak-<key-id>",
    "key_id": "<key-id>",
    "name": "ci-runner",
    "user_id": "user-bob-...",
    "username": "bob",
    "scopes": ["tasks.read"],
    "created_at": "...",
    "expires_at": "...",
    "status": "active"
  },
  "plain_token": "opi_<key-id>_<secret>"
}
```

### `DELETE /api/v1/apikeys/{id}`

Revokes a key. `id` accepts either the row ID (`ak-<keyid>`) or the
bare `key_id`.

**Permission matrix:**

| Key owner    | Requirement                                          |
| ------------ | ---------------------------------------------------- |
| self         | `apikeys.manage.own` **or** `apikeys.manage.all`     |
| another user | `apikeys.manage.all`                                 |

Revocation is a soft state change (`revoked_at` timestamp) — rows are
retained for audit.

## Guardrails

Summarised in one place so operators can see the safety net:

1. **Last-owner protection.** The last user with `role-owner` cannot
   be disabled, deleted, or have `role-owner` revoked. Applies at
   `PATCH /users/{id}`, `DELETE /users/{id}`, and
   `DELETE /users/{id}/roles/role-owner`.
2. **Self-delete block.** `DELETE /users/{id}` with `id` equal to
   the caller's principal returns `409`.
3. **Self-status block.** A user without `users.manage` cannot flip
   their own `status` via `PATCH`.
4. **Password resets.** Resetting another user's password requires
   both `users.manage` **and** `secrets.write`.
5. **Plaintext-once.** API-key plaintext is only returned in the
   `POST` response.
6. **API keys disabled.** If `auth.api_keys.enabled=false`, mint is
   rejected even for the owner.

## Audit events

Every mutation writes an `AuditEntry` (`internal/datastore/types.go`)
with `ResourceType="user"` or `"apikey"`:

| Action               | Triggered by                                 |
| -------------------- | -------------------------------------------- |
| `user.create`        | `POST /users`                                |
| `user.update`        | `PATCH /users/{id}` (any field)              |
| `user.status`        | `PATCH /users/{id}` with status change       |
| `user.password`      | `PATCH /users/{id}` with password change     |
| `user.delete`        | `DELETE /users/{id}`                         |
| `user.role.grant`    | `POST /users/{id}/roles`                     |
| `user.role.revoke`   | `DELETE /users/{id}/roles/{role}`            |
| `apikey.create`      | `POST /apikeys`                              |
| `apikey.revoke`      | `DELETE /apikeys/{id}`                       |

Metadata always contains `path` and `method`; role/key operations
additionally contain `role_id`/`role_name` or `owner_id`/`key_id`/
`scopes`/`name`/`mint_type` (`self`/`delegated`).
