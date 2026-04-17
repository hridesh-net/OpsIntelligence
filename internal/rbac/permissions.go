// Package rbac is OpsIntelligence's role-based access-control engine.
//
// It sits above internal/auth (which only defines identity) and is
// consumed by internal/gateway HTTP middleware, internal/security
// guardrail (tool-call gating), and the admin CLI.
//
// Design goals:
//   - Permissions are dotted, lowercase, namespaced keys ("tasks.read",
//     "users.manage"). Never booleans on the User row — always looked
//     up through a Role.
//   - A trailing "*" wildcards a whole namespace
//     ("tasks.*" grants every tasks.X permission, "*" grants everything).
//   - Built-in roles are declared in Go, re-seeded by every boot, and
//     cannot be deleted. Operators layer custom roles on top.
//   - Enforce() is a pure function that takes a Principal and a
//     Permission. No datastore I/O on the hot path — the
//     principal's Permissions slice is resolved once at login /
//     credential-verify time.
package rbac

import "strings"

// Permission is a dotted, lowercase, namespaced key granted via roles.
// The authoritative list lives below; keeping them as typed constants
// (not ad-hoc strings) catches typos at compile time.
type Permission string

// String returns the string representation (`"tasks.read"`).
func (p Permission) String() string { return string(p) }

// Namespace returns the substring before the first `.`, e.g. "tasks"
// for Permission("tasks.read"). Used by the Enforce matcher.
func (p Permission) Namespace() string {
	s := string(p)
	if i := strings.Index(s, "."); i > 0 {
		return s[:i]
	}
	return s
}

// Matches reports whether a granted permission key satisfies this
// requested permission. Grants may be exact ("tasks.read"), namespace
// wildcards ("tasks.*"), or the global wildcard ("*").
//
// Called in-process per tool call / per request; MUST stay allocation-
// free. No regex, no reflection, no map lookups.
func (p Permission) Matches(granted string) bool {
	if granted == "*" {
		return true
	}
	if granted == string(p) {
		return true
	}
	if strings.HasSuffix(granted, ".*") {
		prefix := granted[:len(granted)-1] // keep trailing "."
		return strings.HasPrefix(string(p), prefix)
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────
// Authoritative permission catalogue
//
// Add new permissions here, grant them through a built-in role below,
// and document them in doc/rbac.md. The linter in rbac_test.go ensures
// every built-in role references only keys that exist in this file.
// ─────────────────────────────────────────────────────────────────────

// Agent — invocation + live control of the running agent process.
const (
	PermAgentInvoke      Permission = "agent.invoke"      // send prompts / call agent tools
	PermAgentInterrupt   Permission = "agent.interrupt"   // cancel a running iteration
	PermAgentConfigRead  Permission = "agent.config.read" // inspect system prompt, model, limits
	PermAgentConfigWrite Permission = "agent.config.write"
)

// Tasks — sub-agent task manager + history.
const (
	PermTasksRead      Permission = "tasks.read"
	PermTasksCancel    Permission = "tasks.cancel"
	PermTasksIntervene Permission = "tasks.intervene" // master-style interventions
	PermTasksDelete    Permission = "tasks.delete"    // purge history rows
)

// Users — identity management.
const (
	PermUsersRead   Permission = "users.read"
	PermUsersManage Permission = "users.manage" // create/edit/disable
	PermUsersDelete Permission = "users.delete"
)

// Roles — RBAC self-management.
const (
	PermRolesRead   Permission = "roles.read"
	PermRolesManage Permission = "roles.manage" // create/edit/delete custom roles + grant
)

// API keys — service-account credentials.
const (
	PermAPIKeysReadOwn   Permission = "apikeys.read.own"   // your own keys
	PermAPIKeysManageOwn Permission = "apikeys.manage.own" // create/revoke your own keys
	PermAPIKeysReadAll   Permission = "apikeys.read.all"
	PermAPIKeysManageAll Permission = "apikeys.manage.all"
)

// Audit log.
const (
	PermAuditRead Permission = "audit.read"
)

// Skills + tool catalogue.
const (
	PermSkillsRead    Permission = "skills.read"
	PermSkillsInstall Permission = "skills.install"
	PermSkillsRemove  Permission = "skills.remove"
	PermToolsRead     Permission = "tools.read"
	PermToolsInvoke   Permission = "tools.invoke" // manual tool invocation via API
)

// Webhooks + channels.
const (
	PermWebhooksRead   Permission = "webhooks.read"
	PermWebhooksManage Permission = "webhooks.manage"
	PermChannelsRead   Permission = "channels.read"
	PermChannelsManage Permission = "channels.manage"
)

// Settings, providers, secrets — the keys of the castle.
const (
	PermSettingsRead   Permission = "settings.read"
	PermSettingsWrite  Permission = "settings.write"
	PermSecretsRead    Permission = "secrets.read"    // masked display only
	PermSecretsWrite   Permission = "secrets.write"   // set API keys / tokens
	PermDatastoreAdmin Permission = "datastore.admin" // run migrations, purge
)

// Dashboard — coarse-grained toggles.
const (
	PermDashboardView Permission = "dashboard.view"
	PermChatUse       Permission = "chat.use"
)

// AllPermissions returns every permission declared above. The returned
// slice is freshly allocated; callers may sort or filter without
// racing other callers.
//
// Kept exhaustive by a test that fails loudly if a new const block is
// added and forgotten here.
func AllPermissions() []Permission {
	return []Permission{
		PermAgentInvoke, PermAgentInterrupt, PermAgentConfigRead, PermAgentConfigWrite,
		PermTasksRead, PermTasksCancel, PermTasksIntervene, PermTasksDelete,
		PermUsersRead, PermUsersManage, PermUsersDelete,
		PermRolesRead, PermRolesManage,
		PermAPIKeysReadOwn, PermAPIKeysManageOwn, PermAPIKeysReadAll, PermAPIKeysManageAll,
		PermAuditRead,
		PermSkillsRead, PermSkillsInstall, PermSkillsRemove,
		PermToolsRead, PermToolsInvoke,
		PermWebhooksRead, PermWebhooksManage, PermChannelsRead, PermChannelsManage,
		PermSettingsRead, PermSettingsWrite, PermSecretsRead, PermSecretsWrite, PermDatastoreAdmin,
		PermDashboardView, PermChatUse,
	}
}
