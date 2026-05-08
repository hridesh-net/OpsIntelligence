package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/drivers"
)

// githubAppCmd groups CLI operations for the GitHub App multi-tenant integration.
func githubAppCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github-app",
		Short: "Manage GitHub App installations and per-org endpoint routing",
		Long: `Manage the OpsIntelligence GitHub App multi-tenant integration.

Organizations install the GitHub App from the GitHub Marketplace (or directly).
After installation they visit the post-install setup page to configure their
on-premise OpsIntelligence endpoint URL and webhook secret.

This command group lets operators inspect and manage those installation records
from the CLI. Run ` + "`opsintelligence github-app installations --help`" + ` for details.`,
	}

	inst := &cobra.Command{
		Use:   "installations",
		Short: "Manage GitHub App installation records",
	}
	inst.AddCommand(githubAppListCmd(gf))
	inst.AddCommand(githubAppShowCmd(gf))
	inst.AddCommand(githubAppSetEndpointCmd(gf))
	inst.AddCommand(githubAppClearEndpointCmd(gf))
	cmd.AddCommand(inst)
	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// list
// ─────────────────────────────────────────────────────────────────────────────

func githubAppListCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all GitHub App installations",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, cleanup, err := ghAppOpenStore(gf)
			if err != nil {
				return err
			}
			defer cleanup()

			installations, err := store.GitHubAppInstallations().List(cmd.Context())
			if err != nil {
				return fmt.Errorf("list installations: %w", err)
			}
			if len(installations) == 0 {
				fmt.Println("No GitHub App installations found.")
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "INSTALLATION ID\tACCOUNT\tTYPE\tACTIVE\tENDPOINT")
			for _, i := range installations {
				endpoint := i.OpsEndpoint
				if endpoint == "" {
					endpoint = "(local)"
				}
				fmt.Fprintf(tw, "%d\t%s\t%s\t%v\t%s\n",
					i.ID, i.AccountLogin, i.AccountType, i.Active, endpoint)
			}
			return tw.Flush()
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// show
// ─────────────────────────────────────────────────────────────────────────────

func githubAppShowCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show <installation_id>",
		Short: "Show details for one GitHub App installation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid installation_id: %w", err)
			}

			store, cleanup, err := ghAppOpenStore(gf)
			if err != nil {
				return err
			}
			defer cleanup()

			inst, err := store.GitHubAppInstallations().Get(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get installation %d: %w", id, err)
			}

			hasSecret := strings.TrimSpace(inst.OpsWebhookSecret) != ""
			fmt.Printf("Installation ID : %d\n", inst.ID)
			fmt.Printf("Account         : %s (%s)\n", inst.AccountLogin, inst.AccountType)
			fmt.Printf("Active          : %v\n", inst.Active)
			fmt.Printf("Ops Endpoint    : %s\n", coalesceStr(inst.OpsEndpoint, "(none — processed locally)"))
			fmt.Printf("Webhook Secret  : %s\n", boolLabel(hasSecret, "configured", "not set"))
			fmt.Printf("Created         : %s\n", inst.CreatedAt.UTC().Format(time.RFC3339))
			fmt.Printf("Updated         : %s\n", inst.UpdatedAt.UTC().Format(time.RFC3339))
			return nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// set-endpoint
// ─────────────────────────────────────────────────────────────────────────────

func githubAppSetEndpointCmd(gf *globalFlags) *cobra.Command {
	var webhookSecret string
	cmd := &cobra.Command{
		Use:   "set-endpoint <installation_id> <endpoint_url>",
		Short: "Set the on-premise OpsIntelligence endpoint for an installation",
		Long: `Configure where GitHub events are relayed for the given installation.

endpoint_url is the base URL of the org's OpsIntelligence instance
(e.g. https://opi.acme.internal). The relay POSTs to <endpoint_url>/api/webhook/github.

--webhook-secret is the value of webhooks.adapters.github.secret in the org's
OpsIntelligence config. The relay re-signs each payload with this secret so the
receiving instance can verify it via X-Hub-Signature-256.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid installation_id: %w", err)
			}
			endpoint := strings.TrimRight(strings.TrimSpace(args[1]), "/")
			if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
				return fmt.Errorf("endpoint_url must start with http:// or https://")
			}

			store, cleanup, err := ghAppOpenStore(gf)
			if err != nil {
				return err
			}
			defer cleanup()

			if err := store.GitHubAppInstallations().SetEndpoint(cmd.Context(), id, endpoint, webhookSecret); err != nil {
				return fmt.Errorf("set endpoint: %w", err)
			}
			fmt.Printf("Installation %d endpoint set to: %s\n", id, endpoint)
			return nil
		},
	}
	cmd.Flags().StringVar(&webhookSecret, "webhook-secret", "",
		"Webhook secret for signing relayed payloads (webhooks.adapters.github.secret in org's config)")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// clear-endpoint
// ─────────────────────────────────────────────────────────────────────────────

func githubAppClearEndpointCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "clear-endpoint <installation_id>",
		Short: "Remove the endpoint for an installation (events processed locally)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid installation_id: %w", err)
			}

			store, cleanup, err := ghAppOpenStore(gf)
			if err != nil {
				return err
			}
			defer cleanup()

			if err := store.GitHubAppInstallations().SetEndpoint(cmd.Context(), id, "", ""); err != nil {
				return fmt.Errorf("clear endpoint: %w", err)
			}
			fmt.Printf("Installation %d endpoint cleared (events will be processed locally).\n", id)
			return nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// shared helpers
// ─────────────────────────────────────────────────────────────────────────────

func ghAppOpenStore(gf *globalFlags) (datastore.Store, func(), error) {
	log := buildLogger(gf.logLevel, "")
	cfg, err := loadConfig(gf.configPath, log)
	if err != nil {
		return nil, func() {}, fmt.Errorf("load config: %w", err)
	}
	dc := cfg.Datastore
	var lifetime time.Duration
	if strings.TrimSpace(dc.ConnMaxLifetime) != "" {
		d, err := time.ParseDuration(dc.ConnMaxLifetime)
		if err != nil {
			return nil, func() {}, fmt.Errorf("datastore conn_max_lifetime: %w", err)
		}
		lifetime = d
	}
	store, err := datastore.Open(context.Background(), datastore.Config{
		Driver:          dc.Driver,
		DSN:             dc.DSN,
		MaxOpenConns:    dc.MaxOpenConns,
		MaxIdleConns:    dc.MaxIdleConns,
		ConnMaxLifetime: lifetime,
		Migrations:      "auto",
	})
	if err != nil {
		return nil, func() {}, fmt.Errorf("open datastore: %w", err)
	}
	return store, func() { _ = store.Close() }, nil
}

func coalesceStr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func boolLabel(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}
