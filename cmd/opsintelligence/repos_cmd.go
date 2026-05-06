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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/opsintelligence/opsintelligence/cmd/opsintelligence/tui"
	"github.com/opsintelligence/opsintelligence/internal/config"
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
		registryFile = "data/repointel/repos.yaml"
	}
	if !filepath.IsAbs(registryFile) {
		registryFile = filepath.Join(cfg.StateDir, registryFile)
	}
	return repointel.NewRegistry(registryFile)
}

func parseOwnerName(full string) (owner, name string, err error) {
	full = strings.TrimSpace(full)
	full = strings.TrimSuffix(full, ".git")
	full = strings.TrimPrefix(full, "https://github.com/")
	full = strings.TrimPrefix(full, "http://github.com/")
	full = strings.TrimPrefix(full, "https://gitlab.com/")
	full = strings.TrimPrefix(full, "http://gitlab.com/")
	if strings.HasPrefix(full, "git@github.com:") {
		full = strings.TrimPrefix(full, "git@github.com:")
	}
	if strings.HasPrefix(full, "git@gitlab.com:") {
		full = strings.TrimPrefix(full, "git@gitlab.com:")
	}
	full = strings.TrimPrefix(full, "/")
	parts := strings.SplitN(full, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must be in owner/name format, got %q", full)
	}
	return parts[0], parts[1], nil
}

// ensureRepoIntelEnabled flips repo_intel.enabled on first use so repos add/sync
// "just works" without requiring manual YAML edits.
func ensureRepoIntelEnabled(gf *globalFlags) (bool, error) {
	cfgPath := strings.TrimSpace(gf.configPath)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	if cfg, err := loadConfig(cfgPath, nil); err == nil && cfg.RepoIntel.Enabled {
		return false, nil
	}
	patch := []byte("repo_intel:\n  enabled: true\n")
	merged, err := mergeOnboardYAML(cfgPath, patch)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(cfgPath, merged, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// ── repos add ─────────────────────────────────────────────────────────────────

func reposAddCmd(gf *globalFlags) *cobra.Command {
	var platform string
	var cloneURL string
	cmd := &cobra.Command{
		Use:   "add <owner/name>",
		Short: "Add a repo to the Repo Intelligence registry",
		Long: `Add a repository to the Repo Intelligence registry for indexing and scanning.

For GitHub repos the GitHub API is used by default (requires a token for private repos).
For GitLab repos, or when --clone-url is provided, the repo is fetched via git clone.

Examples:
  opsintelligence repos add myorg/myrepo
  opsintelligence repos add myorg/myrepo --platform gitlab
  opsintelligence repos add myorg/myrepo --clone-url https://git.internal.company.com/myorg/myrepo.git`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, name, err := parseOwnerName(args[0])
			if err != nil {
				return err
			}
			enabledNow, err := ensureRepoIntelEnabled(gf)
			if err != nil {
				return fmt.Errorf("enable repo_intel: %w", err)
			}
			cfg, err := loadConfig(gf.configPath, nil)
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
				CloneURL: strings.TrimSpace(cloneURL),
				AddedAt:  time.Now(),
			}
			if err := reg.Add(entry); err != nil {
				return err
			}
			if err := reg.UpdateIndexStatus(entry.ID, repointel.IndexPending, "", ""); err != nil {
				return err
			}
			if err := reg.UpdateScanStatus(entry.ID, repointel.ScanPending, "", ""); err != nil {
				return err
			}
			cloneNote := ""
			if entry.CloneURL != "" {
				cloneNote = fmt.Sprintf(" (clone: %s)", entry.CloneURL)
			}
			fmt.Printf("Repo %s/%s added (platform: %s)%s.\n", owner, name, platform, cloneNote)
			mode, syncErr := notifyRepoSyncViaGateway(cfg, entry.ID)
			switch {
			case syncErr != nil:
				fmt.Println("Queued initial indexing + scan in registry.")
				fmt.Printf("Live enqueue unavailable (%v). A running agent will pick it up on poll.\n", syncErr)
			case mode == "live":
				fmt.Println("Queued initial indexing + scan and notified the running manager immediately.")
			default:
				fmt.Println("Queued initial indexing + scan in registry.")
				fmt.Println("The running manager did not accept a live enqueue, so it will pick this up on poll.")
			}
			if enabledNow {
				fmt.Println("Repo Intelligence was disabled in config and has been auto-enabled.")
				fmt.Println("If an agent is already running, restart once: `opsintelligence restart`.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "github", "Platform: github or gitlab")
	cmd.Flags().StringVar(&cloneURL, "clone-url", "", "Git clone URL (overrides auto-inferred URL; enables git clone fetch path)")
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
			enabledNow, err := ensureRepoIntelEnabled(gf)
			if err != nil {
				return fmt.Errorf("enable repo_intel: %w", err)
			}
			cfg, err := loadConfig(gf.configPath, nil)
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
			mode, syncErr := notifyRepoSyncViaGateway(cfg, id)
			switch {
			case syncErr != nil:
				fmt.Printf("Live enqueue unavailable (%v). Falling back to file-backed queue.\n", syncErr)
			case mode == "live":
				fmt.Println("Running manager notified immediately.")
			default:
				fmt.Println("Running manager did not accept a live enqueue; it will pick this up on poll.")
			}
			if enabledNow {
				fmt.Println("Repo Intelligence was disabled in config and has been auto-enabled.")
				fmt.Println("If an agent is already running, restart once: `opsintelligence restart`.")
			}
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
			cfg, cfgErr := loadConfig(gf.configPath, nil)
			if cfgErr == nil && !cfg.RepoIntel.Enabled && (e.IndexStatus == repointel.IndexPending || e.ScanStatus == repointel.ScanPending) {
				fmt.Println("Hint: repo_intel.enabled is false; queue will stay pending until enabled.")
				fmt.Println("Set `repo_intel.enabled: true` then restart: `opsintelligence restart`.")
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
	cfg, err := loadConfig(gf.configPath, nil)
	if err != nil {
		return err
	}

	registryFile := cfg.RepoIntel.RegistryFile
	if registryFile == "" {
		registryFile = "data/repointel/repos.yaml"
	}
	if !filepath.IsAbs(registryFile) {
		registryFile = filepath.Join(cfg.StateDir, registryFile)
	}

	memDir := cfg.RepoIntel.MemoryDir
	if memDir == "" {
		memDir = "data/repointel/memory"
	}
	if !filepath.IsAbs(memDir) {
		memDir = filepath.Join(cfg.StateDir, memDir)
	}

	reg, err := repointel.NewRegistry(registryFile)
	if err != nil {
		return err
	}

	return tui.ReposTUIRun(tui.ReposTUIConfig{
		Registry:  reg,
		MemoryDir: memDir,
		OnSyncRequest: func(id string) {
			_, _ = notifyRepoSyncViaGateway(cfg, id)
		},
	})
}

func notifyRepoSyncViaGateway(cfg *config.Config, repoID string) (string, error) {
	if cfg == nil {
		return "fallback", fmt.Errorf("config not loaded")
	}
	origins := repoSyncNotifyOrigins(cfg)
	if len(origins) == 0 {
		return "fallback", fmt.Errorf("gateway base URL is empty")
	}
	var lastErr error
	for i, base := range origins {
		mode, err := postRepoSyncNotify(cfg, base, repoID)
		if mode == "live" {
			return "live", nil
		}
		if err == nil {
			// e.g. HTTP 404 — do not try another origin.
			return "fallback", nil
		}
		lastErr = err
		if i+1 < len(origins) && isRetryableRepoSyncTransportErr(err) {
			continue
		}
		return "fallback", err
	}
	return "fallback", lastErr
}

// repoSyncNotifyOrigins returns gateway origins to try for live repo sync.
// Loopback is first so CLI enqueue works when gateway.tailscale.mode is funnel
// (PublicGatewayBaseURL is https://*.ts.net:443) but the daemon still listens
// on the local gateway.port — a common cause of "connection refused" on 443.
func repoSyncNotifyOrigins(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	local := loopbackGatewayOrigin(cfg)
	pub := strings.TrimSuffix(strings.TrimSpace(effectiveGatewayOrigin(cfg)), "/")
	var out []string
	if local != "" {
		out = append(out, local)
	}
	if pub != "" && pub != local {
		out = append(out, pub)
	}
	return out
}

func loopbackGatewayOrigin(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	p := cfg.Gateway.Port
	if p == 0 {
		p = 18790
	}
	return fmt.Sprintf("http://127.0.0.1:%d", p)
}

func isRetryableRepoSyncTransportErr(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		switch e := opErr.Err.(type) {
		case syscall.Errno:
			switch e {
			case syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ETIMEDOUT:
				return true
			default:
				return false
			}
		default:
			return false
		}
	}
	return false
}

func postRepoSyncNotify(cfg *config.Config, base, repoID string) (mode string, err error) {
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	if base == "" {
		return "fallback", fmt.Errorf("gateway base URL is empty")
	}
	syncURL := fmt.Sprintf("%s/api/v1/repos/%s/sync", base, repoPathEscape(repoID))
	req, err := http.NewRequest(http.MethodPost, syncURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "fallback", err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := strings.TrimSpace(cfg.Gateway.Token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted:
		return "live", nil
	case http.StatusNotFound:
		return "fallback", nil
	default:
		return "fallback", fmt.Errorf("gateway returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
}

func repoPathEscape(id string) string {
	id = strings.ReplaceAll(id, ":", "%3A")
	id = strings.ReplaceAll(id, "/", "%2F")
	return id
}

var _ = json.Valid
