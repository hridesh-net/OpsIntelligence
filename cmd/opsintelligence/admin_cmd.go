package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// adminCmd groups every identity / RBAC / API-key operation the
// operator might need before (or alongside) the dashboard.
//
// Every subcommand here also has a REST counterpart under /api/v1/
// (phase 3b). CLI and UI both call the same shared service layer
// (phase 3a) so the behaviours stay in lock-step.
func adminCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Manage users, roles, and API keys",
		Long: `Day-2 admin operations for the OpsIntelligence ops plane.

The ` + "`admin`" + ` command group is the CLI twin of the forthcoming Settings
section in the dashboard UI — every operation here will also be
available over the API at /api/v1/... with the same permission
gates. Use ` + "`admin init`" + ` on a fresh deployment to provision the
owner account, then manage everything else from the CLI or the UI.`,
	}
	cmd.AddCommand(adminInitCmd(gf))
	cmd.AddCommand(adminUserCmd(gf))
	cmd.AddCommand(adminRoleCmd(gf))
	cmd.AddCommand(adminAPIKeyCmd(gf))
	return cmd
}

// ─────────────────────────────────────────────────────────────────────
// admin init
// ─────────────────────────────────────────────────────────────────────

func adminInitCmd(gf *globalFlags) *cobra.Command {
	var username, email, password string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Provision the initial owner account on a fresh deployment",
		Long: `Seed the built-in roles and create the first owner user.

Safe to run on an already-initialised deployment — it will detect the
existing owner and exit 0 without changes. Password is read from
stdin when not supplied via --password; never type it inline on
shared hosts.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return fmt.Errorf("open datastore: %w", err)
			}
			defer store.Close()
			if err := store.Migrate(ctx); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}

			n, err := store.Users().Count(ctx)
			if err != nil {
				return err
			}
			if n > 0 {
				fmt.Println("ops plane already initialised, nothing to do")
				fmt.Println("(use `opsintelligence admin user add` to provision more users)")
				return nil
			}

			username = strings.TrimSpace(username)
			if username == "" {
				username = promptLine("owner username [owner]: ")
				if username == "" {
					username = "owner"
				}
			}
			email = strings.TrimSpace(email)
			if email == "" {
				email = promptLine("owner email: ")
			}
			if password == "" {
				var err error
				password, err = promptPasswordConfirm()
				if err != nil {
					return err
				}
			}
			if err := validatePassword(password, cfg.Auth.Local.MinPasswordLength); err != nil {
				return err
			}

			hash, err := auth.HashPassword(password, nil)
			if err != nil {
				return err
			}
			user, created, err := rbac.BootstrapOwner(ctx, store, username, email, hash)
			if err != nil {
				return err
			}
			if !created {
				fmt.Println("ops plane already initialised, nothing to do")
				return nil
			}
			fmt.Printf("provisioned owner %q (%s)\n", user.Username, user.ID)
			fmt.Println("next steps:")
			fmt.Println("  • `opsintelligence admin user add <name>` to onboard additional users")
			fmt.Println("  • `opsintelligence admin apikey create --user " + user.Username + " --name ci` for automation")
			fmt.Println("  • start the gateway and sign in at /")
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "owner username (default: prompt, fallback 'owner')")
	cmd.Flags().StringVar(&email, "email", "", "owner email (default: prompt)")
	cmd.Flags().StringVar(&password, "password", "",
		"owner password (default: prompt). Prefer stdin over this flag on shared hosts.")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────
// admin user <sub>
// ─────────────────────────────────────────────────────────────────────

func adminUserCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Create, list, disable, or delete user accounts",
	}
	cmd.AddCommand(adminUserAddCmd(gf))
	cmd.AddCommand(adminUserListCmd(gf))
	cmd.AddCommand(adminUserDisableCmd(gf))
	cmd.AddCommand(adminUserEnableCmd(gf))
	cmd.AddCommand(adminUserDeleteCmd(gf))
	cmd.AddCommand(adminUserPasswordCmd(gf))
	return cmd
}

func adminUserAddCmd(gf *globalFlags) *cobra.Command {
	var username, email, password, role string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a new user with one initial role",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			username = strings.TrimSpace(username)
			if username == "" {
				return errors.New("--username is required")
			}
			if _, err := store.Users().GetByUsername(ctx, username); err == nil {
				return fmt.Errorf("user %q already exists", username)
			} else if !errors.Is(err, datastore.ErrNotFound) {
				return err
			}
			if password == "" {
				p, err := promptPasswordConfirm()
				if err != nil {
					return err
				}
				password = p
			}
			if err := validatePassword(password, cfg.Auth.Local.MinPasswordLength); err != nil {
				return err
			}
			hash, err := auth.HashPassword(password, nil)
			if err != nil {
				return err
			}
			u := &datastore.User{
				ID:           "user-" + stableID(username),
				Username:     username,
				Email:        strings.TrimSpace(email),
				PasswordHash: hash,
				Status:       datastore.UserActive,
			}
			if err := store.Users().Create(ctx, u); err != nil {
				return err
			}
			if role = strings.TrimSpace(role); role != "" {
				if err := assignRoleByName(ctx, store, u.ID, role); err != nil {
					return err
				}
			}
			fmt.Printf("created user %q (%s)", u.Username, u.ID)
			if role != "" {
				fmt.Printf(" with role %q", role)
			}
			fmt.Println()
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "(required) username")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	cmd.Flags().StringVar(&password, "password", "", "password (default: prompt)")
	cmd.Flags().StringVar(&role, "role", "viewer", "initial role (owner/admin/operator/developer/auditor/viewer or a custom role name)")
	return cmd
}

func adminUserListCmd(gf *globalFlags) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List users",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			if limit <= 0 {
				limit = 100
			}
			users, err := store.Users().List(ctx, limit, 0)
			if err != nil {
				return err
			}
			fmt.Printf("%-24s %-22s %-10s %s\n", "USERNAME", "EMAIL", "STATUS", "ROLES")
			for _, u := range users {
				roles, _ := store.Roles().ListRolesForUser(ctx, u.ID)
				roleNames := make([]string, 0, len(roles))
				for _, r := range roles {
					roleNames = append(roleNames, r.Name)
				}
				fmt.Printf("%-24s %-22s %-10s %s\n", u.Username, u.Email, u.Status, strings.Join(roleNames, ","))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum rows to show")
	return cmd
}

func adminUserDisableCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "disable <username>",
		Short: "Disable a user (keeps audit trail, blocks login and new API keys)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setUserStatusCLI(cmd, gf, args[0], datastore.UserDisabled)
		},
	}
}

func adminUserEnableCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "enable <username>",
		Short: "Re-enable a previously-disabled user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setUserStatusCLI(cmd, gf, args[0], datastore.UserActive)
		},
	}
}

func adminUserDeleteCmd(gf *globalFlags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <username>",
		Short: "Permanently delete a user (audit rows retain the user_id)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !force {
				ans := promptLine(fmt.Sprintf("delete user %q permanently? [y/N]: ", args[0]))
				if strings.ToLower(ans) != "y" && strings.ToLower(ans) != "yes" {
					return nil
				}
			}
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			u, err := store.Users().GetByUsername(ctx, args[0])
			if err != nil {
				return err
			}
			if err := store.Users().Delete(ctx, u.ID); err != nil {
				return err
			}
			fmt.Printf("deleted user %q\n", u.Username)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")
	return cmd
}

func adminUserPasswordCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "password <username>",
		Short: "Set a user's password (reads from stdin prompt)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			u, err := store.Users().GetByUsername(ctx, args[0])
			if err != nil {
				return err
			}
			pw, err := promptPasswordConfirm()
			if err != nil {
				return err
			}
			if err := validatePassword(pw, cfg.Auth.Local.MinPasswordLength); err != nil {
				return err
			}
			hash, err := auth.HashPassword(pw, nil)
			if err != nil {
				return err
			}
			if err := store.Users().SetPassword(ctx, u.ID, hash); err != nil {
				return err
			}
			fmt.Printf("updated password for %q\n", u.Username)
			return nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────
// admin role <sub>
// ─────────────────────────────────────────────────────────────────────

func adminRoleCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "List built-in roles and grant/revoke them to users",
	}
	cmd.AddCommand(adminRoleListCmd(gf))
	cmd.AddCommand(adminRoleGrantCmd(gf))
	cmd.AddCommand(adminRoleRevokeCmd(gf))
	return cmd
}

func adminRoleListCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all roles (built-in + custom) with their permissions",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			if _, _, err := rbac.SeedBuiltInRoles(ctx, store); err != nil {
				return err
			}
			roles, err := store.Roles().List(ctx)
			if err != nil {
				return err
			}
			for _, r := range roles {
				perms, _ := store.Roles().ListPermissions(ctx, r.ID)
				builtin := ""
				if r.IsBuiltIn {
					builtin = " (built-in)"
				}
				fmt.Printf("%s%s\n  %s\n  permissions: %d\n", r.Name, builtin, r.Description, len(perms))
			}
			return nil
		},
	}
}

func adminRoleGrantCmd(gf *globalFlags) *cobra.Command {
	var username, role string
	cmd := &cobra.Command{
		Use:   "grant",
		Short: "Grant a role to a user",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			if _, _, err := rbac.SeedBuiltInRoles(ctx, store); err != nil {
				return err
			}
			u, err := store.Users().GetByUsername(ctx, strings.TrimSpace(username))
			if err != nil {
				return err
			}
			if err := assignRoleByName(ctx, store, u.ID, role); err != nil {
				return err
			}
			fmt.Printf("granted %q to %q\n", role, u.Username)
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "user", "", "(required) target username")
	cmd.Flags().StringVar(&role, "role", "", "(required) role name")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func adminRoleRevokeCmd(gf *globalFlags) *cobra.Command {
	var username, role string
	cmd := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke a role from a user",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			u, err := store.Users().GetByUsername(ctx, strings.TrimSpace(username))
			if err != nil {
				return err
			}
			r, err := store.Roles().GetByName(ctx, role)
			if err != nil {
				return err
			}
			if err := store.Roles().RevokeFromUser(ctx, u.ID, r.ID); err != nil {
				return err
			}
			fmt.Printf("revoked %q from %q\n", role, u.Username)
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "user", "", "(required) target username")
	cmd.Flags().StringVar(&role, "role", "", "(required) role name")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────
// admin apikey <sub>
// ─────────────────────────────────────────────────────────────────────

func adminAPIKeyCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apikey",
		Short: "Create, list, or revoke API keys",
	}
	cmd.AddCommand(adminAPIKeyCreateCmd(gf))
	cmd.AddCommand(adminAPIKeyListCmd(gf))
	cmd.AddCommand(adminAPIKeyRevokeCmd(gf))
	return cmd
}

func adminAPIKeyCreateCmd(gf *globalFlags) *cobra.Command {
	var username, name, expires string
	var scopes []string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Mint a new API key for a user (token shown ONCE)",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			u, err := store.Users().GetByUsername(ctx, strings.TrimSpace(username))
			if err != nil {
				return err
			}
			pt, err := auth.GenerateAPIKey(u.ID, strings.TrimSpace(name), scopes)
			if err != nil {
				return err
			}
			// Resolve expiry: CLI flag wins, then AuthConfig.APIKeys.DefaultExpiry.
			exp := strings.TrimSpace(expires)
			if exp == "" {
				exp = strings.TrimSpace(cfg.Auth.APIKeys.DefaultExpiry)
			}
			if exp != "" {
				d, err := time.ParseDuration(exp)
				if err != nil {
					return fmt.Errorf("--expires: %w", err)
				}
				t := time.Now().Add(d).UTC()
				pt.Record.ExpiresAt = &t
			}
			if err := store.APIKeys().Create(ctx, pt.Record); err != nil {
				return err
			}
			fmt.Println("api key created — store this token now, it will NOT be shown again:")
			fmt.Println()
			fmt.Printf("  %s\n", pt.PlainToken)
			fmt.Println()
			fmt.Printf("key_id:  %s\n", pt.Record.KeyID)
			fmt.Printf("user:    %s\n", u.Username)
			fmt.Printf("name:    %s\n", pt.Record.Name)
			if pt.Record.ExpiresAt != nil {
				fmt.Printf("expires: %s\n", pt.Record.ExpiresAt.Format(time.RFC3339))
			}
			if len(pt.Record.Scopes) > 0 {
				fmt.Printf("scopes:  %s\n", strings.Join(pt.Record.Scopes, ","))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "user", "", "(required) owner username")
	cmd.Flags().StringVar(&name, "name", "", "(required) display name")
	cmd.Flags().StringVar(&expires, "expires", "", "expiry as Go duration (e.g. 720h). Empty = no expiry")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil,
		"optional permission scopes (intersected with the owner's perms). Repeatable.")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func adminAPIKeyListCmd(gf *globalFlags) *cobra.Command {
	var username string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List API keys (all by default; --user to scope)",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			var keys []datastore.APIKey
			if username = strings.TrimSpace(username); username != "" {
				u, err := store.Users().GetByUsername(ctx, username)
				if err != nil {
					return err
				}
				keys, err = store.APIKeys().ListForUser(ctx, u.ID)
				if err != nil {
					return err
				}
			} else {
				keys, err = store.APIKeys().ListAll(ctx, 200, 0)
				if err != nil {
					return err
				}
			}
			fmt.Printf("%-12s %-18s %-20s %-22s %s\n", "KEY_ID", "NAME", "OWNER", "EXPIRES", "STATUS")
			for _, k := range keys {
				owner := "?"
				if u, err := store.Users().Get(ctx, k.UserID); err == nil {
					owner = u.Username
				}
				exp := "never"
				if k.ExpiresAt != nil {
					exp = k.ExpiresAt.Format(time.RFC3339)
				}
				status := "active"
				switch {
				case k.RevokedAt != nil:
					status = "revoked"
				case k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()):
					status = "expired"
				}
				fmt.Printf("%-12s %-18s %-20s %-22s %s\n", k.KeyID, k.Name, owner, exp, status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "user", "", "restrict to a single user")
	return cmd
}

func adminAPIKeyRevokeCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <key_id>",
		Short: "Revoke an API key by its public key_id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			k, err := store.APIKeys().GetByKeyID(ctx, args[0])
			if err != nil {
				return err
			}
			if err := store.APIKeys().Revoke(ctx, k.ID); err != nil {
				return err
			}
			fmt.Printf("revoked key %s (%s)\n", k.KeyID, k.Name)
			return nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────

// assignRoleByName looks up the role (built-in or custom) and grants
// it to userID. Re-seeds built-in roles first so a freshly-migrated
// deployment does not 404 on "admin".
func assignRoleByName(ctx context.Context, store datastore.Store, userID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("role name is required")
	}
	if _, _, err := rbac.SeedBuiltInRoles(ctx, store); err != nil {
		return err
	}
	r, err := store.Roles().GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("role %q: %w", name, err)
	}
	return store.Roles().AssignToUser(ctx, userID, r.ID)
}

func setUserStatusCLI(cmd *cobra.Command, gf *globalFlags, username string, status datastore.UserStatus) error {
	log := buildLogger(gf.logLevel)
	cfg, err := loadConfig(gf.configPath, log)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	store, err := openDatastoreFromConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	u, err := store.Users().GetByUsername(ctx, username)
	if err != nil {
		return err
	}
	if err := store.Users().SetStatus(ctx, u.ID, status); err != nil {
		return err
	}
	fmt.Printf("user %q status = %s\n", u.Username, status)
	return nil
}

// validatePassword enforces the configured min length and rejects
// common weak forms. Kept CLI-local on purpose — when configsvc
// (phase 3a) lands it will pull this out so the UI enforces the same
// rules with the same error messages.
func validatePassword(pw string, minLen int) error {
	if minLen <= 0 {
		minLen = 12
	}
	if len(pw) < minLen {
		return fmt.Errorf("password must be at least %d characters", minLen)
	}
	if strings.TrimSpace(pw) != pw {
		return errors.New("password must not have leading/trailing whitespace")
	}
	return nil
}

// stableID derives an 8-char slug from a username so we can produce
// human-grep'able user IDs without a UUID dependency for CLI use.
// Datastore rows keep the derived ID; the username is separately
// unique so two users named "alice" never collide.
func stableID(username string) string {
	// Use the crypto/rand-backed random token so restarts do not
	// regenerate the same ID if we ever retry; collisions on the
	// username field are caught by the unique constraint.
	tok, err := auth.RandomToken(6)
	if err != nil {
		return strings.ToLower(strings.ReplaceAll(username, " ", "-"))
	}
	return strings.ToLower(strings.ReplaceAll(username, " ", "-")) + "-" + tok[:8]
}

// promptLine reads a single trimmed line from stdin.
func promptLine(prompt string) string {
	fmt.Print(prompt)
	rdr := bufio.NewReader(os.Stdin)
	line, _ := rdr.ReadString('\n')
	return strings.TrimSpace(line)
}

// promptPasswordConfirm reads a password twice without echoing and
// returns it only when the two entries match. When stdin is not a
// TTY (CI, scripts), it falls back to a single un-echoed read.
func promptPasswordConfirm() (string, error) {
	fd := int(syscall.Stdin)
	if !term.IsTerminal(fd) {
		rdr := bufio.NewReader(os.Stdin)
		line, err := rdr.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "password: ")
	p1, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "confirm:  ")
	p2, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(p1) != string(p2) {
		return "", errors.New("passwords did not match")
	}
	return string(p1), nil
}
