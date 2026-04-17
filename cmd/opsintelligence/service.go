// Package main — service command: register OpsIntelligence with the OS init system.
package main

import (
	"github.com/spf13/cobra"
)

// serviceCmd returns the `opsintelligence service` command group.
func serviceCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install a background service so OpsIntelligence survives reboot (OS-specific)",
		Long: `Registers OpsIntelligence with the OS so it can start without you running a terminal.

• macOS: LaunchAgent (starts after you log in).
• Linux: systemd user unit (starts when your user session is active; see "opsintelligence service install" output for boot-without-login).
• Windows: prints Task Scheduler steps.

If you have not run "opsintelligence service install", OpsIntelligence will not start automatically after restart.`,
	}
	cmd.AddCommand(serviceInstallCmd(gf))
	cmd.AddCommand(serviceUninstallCmd(gf))
	cmd.AddCommand(serviceStatusCmd(gf))
	cmd.AddCommand(serviceLogsCmd(gf))
	return cmd
}
