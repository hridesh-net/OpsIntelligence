package rbac

// Built-in role names. Kept as typed constants so the CLI + tests can
// reference them symbolically. Custom roles are free-form strings
// defined in the datastore.
const (
	RoleOwner     = "owner"     // ultimate admin, created on first boot
	RoleAdmin     = "admin"     // everything short of owner-only actions
	RoleOperator  = "operator"  // can drive the agent + manage day-2 ops
	RoleDeveloper = "developer" // engineers: chat + tasks, read-only admin
	RoleAuditor   = "auditor"   // read-only across the board + audit log
	RoleViewer    = "viewer"    // read-only dashboard, no agent invoke
)

// RoleSpec is the in-code description of a built-in role. The
// datastore persists a matching (roles, role_permissions) row for each
// entry in BuiltInRoles(); subsequent boots re-upsert them so adding
// a new permission to a built-in role is a Go-code change, not a
// datastore migration.
type RoleSpec struct {
	// ID is the stable, never-rewritten identifier ("role-owner").
	ID string
	// Name matches the constants above.
	Name string
	// Description is human-readable, shown in the Roles UI.
	Description string
	// Permissions grants. Wildcards ("*", "tasks.*") are allowed and
	// expanded on match by Permission.Matches.
	Permissions []Permission
}

// BuiltInRoles returns the canonical list of built-in roles in the
// order the seeder should create them. Owner always ships first so
// first-run bootstrap can hand it to the initial user.
func BuiltInRoles() []RoleSpec {
	return []RoleSpec{
		{
			ID:          "role-owner",
			Name:        RoleOwner,
			Description: "Ultimate administrator. Bypasses every permission check.",
			// Single wildcard is both a capability grant AND a signal
			// to the engine to short-circuit checks.
			Permissions: []Permission{"*"},
		},
		{
			ID:          "role-admin",
			Name:        RoleAdmin,
			Description: "Day-to-day administrator. Manage users, roles, keys, settings, skills.",
			Permissions: []Permission{
				PermDashboardView, PermChatUse,
				PermAgentInvoke, PermAgentInterrupt, PermAgentConfigRead, PermAgentConfigWrite,
				PermTasksRead, PermTasksCancel, PermTasksIntervene, PermTasksDelete,
				PermUsersRead, PermUsersManage,
				PermRolesRead, PermRolesManage,
				PermAPIKeysReadAll, PermAPIKeysManageAll, PermAPIKeysReadOwn, PermAPIKeysManageOwn,
				PermAuditRead,
				PermSkillsRead, PermSkillsInstall, PermSkillsRemove,
				PermToolsRead, PermToolsInvoke,
				PermWebhooksRead, PermWebhooksManage,
				PermChannelsRead, PermChannelsManage,
				PermSettingsRead, PermSettingsWrite, PermSecretsRead,
			},
		},
		{
			ID:          "role-operator",
			Name:        RoleOperator,
			Description: "Operate the running agent: invoke, intervene, manage tasks and webhooks.",
			Permissions: []Permission{
				PermDashboardView, PermChatUse,
				PermAgentInvoke, PermAgentInterrupt, PermAgentConfigRead,
				PermTasksRead, PermTasksCancel, PermTasksIntervene,
				PermAPIKeysReadOwn, PermAPIKeysManageOwn,
				PermAuditRead,
				PermSkillsRead,
				PermToolsRead, PermToolsInvoke,
				PermWebhooksRead,
				PermChannelsRead,
				PermSettingsRead,
			},
		},
		{
			ID:          "role-developer",
			Name:        RoleDeveloper,
			Description: "Engineer: chat with the agent, inspect tasks, read configs. No destructive actions.",
			Permissions: []Permission{
				PermDashboardView, PermChatUse,
				PermAgentInvoke, PermAgentInterrupt, PermAgentConfigRead,
				PermTasksRead,
				PermAPIKeysReadOwn, PermAPIKeysManageOwn,
				PermSkillsRead,
				PermToolsRead,
				PermWebhooksRead,
				PermChannelsRead,
				PermSettingsRead,
			},
		},
		{
			ID:          "role-auditor",
			Name:        RoleAuditor,
			Description: "Read-only across the board, including the audit log. Cannot invoke the agent.",
			Permissions: []Permission{
				PermDashboardView,
				PermTasksRead,
				PermUsersRead,
				PermRolesRead,
				PermAPIKeysReadAll,
				PermAuditRead,
				PermSkillsRead,
				PermToolsRead,
				PermWebhooksRead,
				PermChannelsRead,
				PermSettingsRead,
			},
		},
		{
			ID:          "role-viewer",
			Name:        RoleViewer,
			Description: "Read-only dashboard access. Cannot invoke the agent or inspect audit log.",
			Permissions: []Permission{
				PermDashboardView,
				PermTasksRead,
				PermSkillsRead,
				PermToolsRead,
				PermWebhooksRead,
				PermChannelsRead,
			},
		},
	}
}

// BuiltInRoleSpec returns the spec for the named built-in role, or nil
// if name doesn't match a built-in. Used by the admin CLI's `grant`
// subcommand and by tests.
func BuiltInRoleSpec(name string) *RoleSpec {
	for _, r := range BuiltInRoles() {
		if r.Name == name {
			spec := r
			return &spec
		}
	}
	return nil
}

// IsBuiltInRole reports whether name refers to one of the shipped
// roles. Used to guard against accidental deletion/rename.
func IsBuiltInRole(name string) bool { return BuiltInRoleSpec(name) != nil }
