package main

import (
	"fmt"
	"os"

	"github.com/opsintelligence/opsintelligence/cmd/opsintelligence/tui"
	"github.com/spf13/cobra"
)

func guidesCmd(flags *globalFlags) *cobra.Command {
	github := &cobra.Command{
		Use:   "github",
		Short: "Cheat sheet: GitHub PAT vs webhook secret vs gh for PR reviews",
		Long: `Explains which GitHub-related credentials go where:

  • devops.github — PAT for REST tools (read PR, diff, CI).
  • webhooks.adapters.github — HMAC secret for signed webhook deliveries.
  • gh + GH_TOKEN — optional; posting pull request reviews back to GitHub.

Also lists dashboard paths and doc links.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if flags.noColor || tui.ArgsWantNoColor() || os.Getenv("NO_COLOR") != "" {
				fmt.Print(tui.GitHubSetupGuidePlain())
				return nil
			}
			fmt.Print(tui.RenderGitHubSetupGuide())
			return nil
		},
	}

	root := &cobra.Command{
		Use:   "guides",
		Short: "Short setup cheat sheets (GitHub, webhooks, credentials)",
		Long:  `Prints plain-language guides for common setup tasks. Use subcommands for each topic.`,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	root.AddCommand(github)
	return root
}
