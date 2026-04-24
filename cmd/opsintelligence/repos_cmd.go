package main

// repos_cmd.go — `opsintelligence repos` subcommand.
//
// Sub-commands:
//
//	repos add    <owner/name> [--platform github|gitlab]  Add a repo.
//	repos list                                             List all repos.
//	repos remove <owner/name>                              Remove a repo.
//	repos sync   <owner/name>                              Re-index + re-scan.
//	repos status <owner/name>                              Show current status.
//	repos users  <owner/name> add <handle> --role <role>   Add a user.
//	repos users  <owner/name> remove <handle>              Remove a user.
//	repos tui                                               Open interactive TUI.

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/cmd/opsintelligence/tui"
	"github.com/opsintelligence/opsintelligence/internal/repointel"
	"github.com/spf13/cobra"
)

func reposCmd(gf *globalFlags) *cobra.Command {
	root := &cobra.Command{
		Use:   "repos",
		Short: "Manage Repo Intelligence: index codebases, run scans, manage users",
		Long: `Configure repositories so OpsIntelligence can learn from their codebases.

Once a repo is added the agent will:
  1. Fetch and index key source files via the GitHub API.
  2. Run a CVE + bottleneck scan using the configured LLM.
  3. Inject the resulting memory into all future PR reviews for that repo.

Examples:
  opsintelligence repos add myorg/myrepo
  opsintelligence repos list
  opsintelligence repos status myorg/myrepo
  opsintelligence repos sync myorg/myrepo
  opsintelligence repos users myorg/myrepo add alice --role maintainer
  opsintelligence repos tui`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReposTUI(gf)
		},
	}

	root.AddCommand(
		reposAddCmd(gf),
		reposListCmd(gf),
		reposRemoveCmd(gf),
		reposSyncCmd(gf),
		reposStatusCmd(gf),
		reposUsersCmd(gf),
		reposTUICmd(gf),
	)
	return root
}

// ── helpers ───────────────────────────────────────────────────────────────────

func openRegistry(gf *globalFlags) (*repointel.Registry, error) {
	cfg, err := loadConfig(gf.configPath, nil)
	if err != nil {
		return nil, err
	}
	registryFile := cfg.RepoIntel.RegistryFile
	if registryFile == "" {
		registryFile = "repointel/repos.yaml"
	}
	if !filepath.IsAbs(registryFile) {
		registryFile = filepath.Join(cfg.StateDir, registryFile)
	}
	return repointel.NewRegistry(registryFile)
}

func parseOwnerName(full string) (owner, name string, err error) {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must be in owner/name format, got %q", full)
	}
	return parts[0], parts[1], nil
}

// ── repos add ─────────────────────────────────────────────────────────────────

func reposAddCmd(gf *globalFlags) *cobra.Command {
	var platform string
	cmd := &cobra.Command{
		Use:   "add <owner/name>",
		Short: "Add a repo to the Repo Intelligence registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, name, err := parseOwnerName(args[0])
			if err != nil {
				return err
			}
			reg, err := openRegistry(gf)
			if err != nil {
				return err
			}
			entry := repointel.RepoEntry{
				ID:       repointel.RepoID(platform, owner, name),
				Platform: platform,
				Owner:    owner,
				Name:     name,
				FullName: owner + "/" + name,
				AddedAt:  time.Now(),
			}
			if err := reg.Add(entry); err != nil {
				return err
			}
			fmt.Printf("Repo %s/%s added (platform: %s).\n", owner, name, platform)
			fmt.Println("Run `opsintelligence repos sync` or start the agent to begin indexing.")
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "github", "Platform: github or gitlab")
	return cmd
}

// ── repos list ────────────────────────────────────────────────────────────────

func reposListCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configured repos",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := openRegistry(gf)
			if err != nil {
				return err
			}
			entries := reg.List()
			if len(entries) == 0 {
				fmt.Println("No repos configured. Use `repos add <owner/name>` to add one.")
				return nil
			}
			fmt.Printf("%-40s %-10s %-10s %-8s\n", "REPO", "INDEX", "SCAN", "RISK")
			fmt.Println(strings.Repeat("─", 72))
			for _, e := range entries {
				fmt.Printf("%-40s %-10s %-10s %-8s\n",
					e.FullName,
					string(e.IndexStatus),
					string(e.ScanStatus),
					e.RiskLevel,
				)
			}
			return nil
		},
	}
}

// ── repos remove ──────────────────────────────────────────────────────────────

func reposRemoveCmd(gf *globalFlags) *cobra.Command {
	var platform string
	cmd := &cobra.Command{
		Use:   "remove <owner/name>",
		Short: "Remove a repo from the registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, name, err := parseOwnerName(args[0])
			if err != nil {
				return err
			}
			reg, err := openRegistry(gf)
			if err != nil {
				return err
			}
			id := repointel.RepoID(platform, owner, name)
			if err := reg.Remove(id); err != nil {
				return err
			}
			fmt.Printf("Repo %s/%s removed.\n", owner, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "github", "Platform: github or gitlab")
	return cmd
}

// ── repos sync ────────────────────────────────────────────────────────────────

func reposSyncCmd(gf *globalFlags) *cobra.Command {
	var platform string
	cmd := &cobra.Command{
		Use:   "sync <owner/name>",
		Short: "Re-index and re-scan a repo (requires the agent to be running)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, name, err := parseOwnerName(args[0])
			if err != nil {
				return err
			}
			reg, err := openRegistry(gf)
			if err != nil {
				return err
			}
			id := repointel.RepoID(platform, owner, name)
			// Mark status as pending so the running manager re-processes it.
			if err := reg.UpdateIndexStatus(id, repointel.IndexPending, "", ""); err != nil {
				return err
			}
			if err := reg.UpdateScanStatus(id, repointel.ScanPending, "", ""); err != nil {
				return err
			}
			fmt.Printf("Repo %s/%s queued for re-sync.\n", owner, name)
			fmt.Println("The agent will pick it up on the next processing cycle.")
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "github", "Platform: github or gitlab")
	return cmd
}

// ── repos status ──────────────────────────────────────────────────────────────

func reposStatusCmd(gf *globalFlags) *cobra.Command {
	var platform string
	cmd := &cobra.Command{
		Use:   "status <owner/name>",
		Short: "Show the current indexing and scan status for a repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, name, err := parseOwnerName(args[0])
			if err != nil {
				return err
			}
			reg, err := openRegistry(gf)
			if err != nil {
				return err
			}
			id := repointel.RepoID(platform, owner, name)
			e, err := reg.Get(id)
			if err != nil {
				return err
			}
			fmt.Printf("Repo:          %s/%s\n", e.Owner, e.Name)
			fmt.Printf("Platform:      %s\n", e.Platform)
			fmt.Printf("Added:         %s\n", e.AddedAt.Format(time.RFC3339))
			fmt.Printf("Index status:  %s\n", e.IndexStatus)
			if e.IndexStatus == repointel.IndexReady {
				fmt.Printf("  Indexed at:  %s\n", e.IndexedAt.Format(time.RFC3339))
				fmt.Printf("  Head SHA:    %s\n", e.HeadSHA)
			}
			if e.IndexError != "" {
				fmt.Printf("  Error:       %s\n", e.IndexError)
			}
			fmt.Printf("Scan status:   %s\n", e.ScanStatus)
			if e.ScanStatus == repointel.ScanDone {
				fmt.Printf("  Scanned at:  %s\n", e.ScannedAt.Format(time.RFC3339))
				fmt.Printf("  Risk level:  %s\n", e.RiskLevel)
			}
			if e.ScanError != "" {
				fmt.Printf("  Error:       %s\n", e.ScanError)
			}
			if len(e.Users) > 0 {
				fmt.Printf("Users (%d):\n", len(e.Users))
				for _, u := range e.Users {
					fmt.Printf("  %-20s %s\n", u.Handle, u.Role)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "github", "Platform: github or gitlab")
	return cmd
}

// ── repos users ───────────────────────────────────────────────────────────────

func reposUsersCmd(gf *globalFlags) *cobra.Command {
	root := &cobra.Command{
		Use:   "users <owner/name>",
		Short: "Manage users for a repo",
	}
	root.AddCommand(reposUsersAddCmd(gf), reposUsersRemoveCmd(gf), reposUsersListCmd(gf))
	return root
}

func reposUsersAddCmd(gf *globalFlags) *cobra.Command {
	var role, platform, email string
	cmd := &cobra.Command{
		Use:   "add <owner/name> <handle>",
		Short: "Add a user to a repo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, name, err := parseOwnerName(args[0])
			if err != nil {
				return err
			}
			reg, err := openRegistry(gf)
			if err != nil {
				return err
			}
			id := repointel.RepoID(platform, owner, name)
			u := repointel.RepoUser{
				Handle:  args[1],
				Role:    repointel.UserRole(role),
				Email:   email,
				AddedAt: time.Now(),
			}
			if err := reg.AddUser(id, u); err != nil {
				return err
			}
			fmt.Printf("User %s added to %s/%s with role %s.\n", args[1], owner, name, role)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "contributor", "Role: admin, maintainer, reviewer, contributor")
	cmd.Flags().StringVar(&email, "email", "", "Optional email address")
	cmd.Flags().StringVar(&platform, "platform", "github", "Platform: github or gitlab")
	return cmd
}

func reposUsersRemoveCmd(gf *globalFlags) *cobra.Command {
	var platform string
	cmd := &cobra.Command{
		Use:   "remove <owner/name> <handle>",
		Short: "Remove a user from a repo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, name, err := parseOwnerName(args[0])
			if err != nil {
				return err
			}
			reg, err := openRegistry(gf)
			if err != nil {
				return err
			}
			id := repointel.RepoID(platform, owner, name)
			if err := reg.RemoveUser(id, args[1]); err != nil {
				return err
			}
			fmt.Printf("User %s removed from %s/%s.\n", args[1], owner, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "github", "Platform: github or gitlab")
	return cmd
}

func reposUsersListCmd(gf *globalFlags) *cobra.Command {
	var platform string
	cmd := &cobra.Command{
		Use:   "list <owner/name>",
		Short: "List users for a repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, name, err := parseOwnerName(args[0])
			if err != nil {
				return err
			}
			reg, err := openRegistry(gf)
			if err != nil {
				return err
			}
			id := repointel.RepoID(platform, owner, name)
			e, err := reg.Get(id)
			if err != nil {
				return err
			}
			if len(e.Users) == 0 {
				fmt.Println("No users configured for this repo.")
				return nil
			}
			fmt.Printf("%-20s %-12s %s\n", "HANDLE", "ROLE", "EMAIL")
			fmt.Println(strings.Repeat("─", 50))
			for _, u := range e.Users {
				fmt.Printf("%-20s %-12s %s\n", u.Handle, string(u.Role), u.Email)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "github", "Platform: github or gitlab")
	return cmd
}

// ── repos tui ────────────────────────────────────────────────────────────────

func reposTUICmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive Repo Intelligence TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReposTUI(gf)
		},
	}
}

func runReposTUI(gf *globalFlags) error {
	reg, err := openRegistry(gf)
	if err != nil {
		return err
	}
	return tui.ReposTUIRun(reg)
}
