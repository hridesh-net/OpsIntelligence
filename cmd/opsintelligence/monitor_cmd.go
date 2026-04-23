package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/opsintelligence/opsintelligence/cmd/opsintelligence/tui"
)

func monitorCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "monitor",
		Short: "Live terminal dashboard for monitoring agent runs and system stats",
		Long: `Launches a Bubble Tea TUI that tails the runtrace.ndjson log file and 
displays real-time system metrics (CPU/RAM).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}

			// Get PID and log path
			pid, _ := ReadPID(PidFile(cfg.StateDir))
			logPath := cfg.Agent.RunTraceFile
			if !filepath.IsAbs(logPath) {
				logPath = filepath.Join(cfg.StateDir, logPath)
			}

			if _, err := os.Stat(logPath); os.IsNotExist(err) {
				// Ensure log directory exists
				if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
					return fmt.Errorf("failed to create log dir: %w", err)
				}
				// Create empty file so tailing doesn't fail
				os.WriteFile(logPath, []byte(""), 0644)
			}

			// Info for stats
			return tui.RunMonitor(tui.StatusInfo{
				PID:           pid,
				Version:       version,
				RunTraceFile:  logPath,
				RunTraceMode:  cfg.Agent.RunTraceMode,
			}, logPath)
		},
	}
}
