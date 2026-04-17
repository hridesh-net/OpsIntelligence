//go:build windows

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func installService() error {
	fmt.Println("To run OpsIntelligence on login on Windows, use Task Scheduler:")
	fmt.Println("  1. Open Task Scheduler → Create Basic Task")
	fmt.Println("  2. Trigger: When I log on")
	fmt.Println("  3. Action: Start a program → opsintelligence.exe start")
	return nil
}

func uninstallService() error {
	fmt.Println("Remove the OpsIntelligence task from Windows Task Scheduler manually.")
	return nil
}

func tailServiceLogs() error {
	fmt.Println("Logs are written to %USERPROFILE%\\.opsintelligence\\logs\\opsintelligence.log")
	return nil
}

func serviceInstallCmd(_ *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Instructions for registering OpsIntelligence on Windows login",
		RunE:  func(cmd *cobra.Command, args []string) error { return installService() },
	}
}

func serviceUninstallCmd(_ *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Instructions for removing OpsIntelligence from Windows startup",
		RunE:  func(cmd *cobra.Command, args []string) error { return uninstallService() },
	}
}

func serviceLogsCmd(_ *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Print path to OpsIntelligence log file",
		RunE:  func(cmd *cobra.Command, args []string) error { return tailServiceLogs() },
	}
}

func serviceStatusCmd(_ *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Windows does not register a service automatically",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("OpsIntelligence has no built-in Windows service. Use Task Scheduler (see opsintelligence service install).")
			return nil
		},
	}
}
