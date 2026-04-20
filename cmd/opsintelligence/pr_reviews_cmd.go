package main

// pr_reviews_cmd.go — `opsintelligence pr-reviews` subcommand.
//
// Sub-commands:
//
//   pr-reviews list              List all PR review tasks (default).
//   pr-reviews events <task_id> [--since N]  Full event log for a task.
//   pr-reviews cancel <task_id>  Request cancellation of a task.
//
// All sub-commands hit the running gateway at cfg.Gateway and exit with
// a non-zero status when the gateway is unreachable or returns an error.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func prReviewsCmd(gf *globalFlags) *cobra.Command {
	root := &cobra.Command{
		Use:   "pr-reviews",
		Short: "Monitor and control the PR review sub-agent pool",
		Long: `Inspect and control the parallel PR review pool.

The pool runs at most devops.github.pr_review_workers reviews concurrently
(default 4). Additional reviews queue until a slot is free.

Examples:
  opsintelligence pr-reviews              # list all tasks
  opsintelligence pr-reviews list         # same as above
  opsintelligence pr-reviews events abc12 # event log for task abc12
  opsintelligence pr-reviews cancel abc12 # cancel a pending or running task`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPRReviewsList(gf)
		},
	}

	root.AddCommand(
		prReviewsListCmd(gf),
		prReviewsEventsCmd(gf),
		prReviewsCancelCmd(gf),
	)
	return root
}

// ── list ─────────────────────────────────────────────────────────────────────

func prReviewsListCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all PR review tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPRReviewsList(gf)
		},
	}
}

func runPRReviewsList(gf *globalFlags) error {
	log := buildLogger(gf.logLevel)
	cfg, err := loadConfig(gf.configPath, log)
	if err != nil {
		return err
	}
	base := cfg.PublicGatewayBaseURL()
	body, err := prReviewsGET(base+"/api/v1/pr-reviews", cfg.Gateway.Token)
	if err != nil {
		return fmt.Errorf("pr-reviews list: %w", err)
	}

	var resp struct {
		MaxConcurrent int `json:"max_concurrent"`
		Tasks         []struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			Name       string `json:"name"`
			Elapsed    string `json:"elapsed"`
			LastPhase  string `json:"last_phase"`
			LastEvent  string `json:"last_event"`
			Error      string `json:"error"`
			StartedAt  string `json:"started_at"`
			FinishedAt string `json:"finished_at"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("pr-reviews list: parse response: %w", err)
	}

	if len(resp.Tasks) == 0 {
		fmt.Printf("PR Review Pool  (max %d concurrent)  — no tasks yet.\n", resp.MaxConcurrent)
		fmt.Println("  Use /pr-review: <url>  in chat to start a review.")
		return nil
	}

	fmt.Printf("PR Review Pool  (max %d concurrent)  — %d tasks\n\n",
		resp.MaxConcurrent, len(resp.Tasks))

	for _, t := range resp.Tasks {
		icon := taskIcon(t.Status)
		fmt.Printf("%s  %-12s  %-10s  %-10s  %s\n",
			icon, t.ID, t.Status, t.Elapsed, t.Name)
		if t.LastEvent != "" {
			phase := t.LastPhase
			if phase == "" {
				phase = "-"
			}
			fmt.Printf("              ↳ [%s] %s\n", phase, t.LastEvent)
		}
		if t.Error != "" {
			fmt.Printf("              ✗ error: %s\n", t.Error)
		}
	}
	return nil
}

// ── events ────────────────────────────────────────────────────────────────────

func prReviewsEventsCmd(gf *globalFlags) *cobra.Command {
	var since int
	cmd := &cobra.Command{
		Use:   "events <task_id>",
		Short: "Show the full event log for a PR review task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPRReviewsEvents(gf, args[0], since)
		},
	}
	cmd.Flags().IntVar(&since, "since", 0, "Skip the first N events (for incremental polling)")
	return cmd
}

func runPRReviewsEvents(gf *globalFlags, taskID string, since int) error {
	log := buildLogger(gf.logLevel)
	cfg, err := loadConfig(gf.configPath, log)
	if err != nil {
		return err
	}
	base := cfg.PublicGatewayBaseURL()
	url := fmt.Sprintf("%s/api/v1/pr-reviews/%s/events?since=%d", base, taskID, since)
	body, err := prReviewsGET(url, cfg.Gateway.Token)
	if err != nil {
		return fmt.Errorf("pr-reviews events: %w", err)
	}

	var resp struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
		Since  int    `json:"since"`
		Events []struct {
			At      string `json:"at"`
			Kind    string `json:"kind"`
			Phase   string `json:"phase"`
			Message string `json:"message"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("pr-reviews events: parse response: %w", err)
	}

	fmt.Printf("PR Review task=%s  status=%s\n\n", resp.TaskID, resp.Status)
	if len(resp.Events) == 0 {
		fmt.Printf("  (no new events since index %d)\n", resp.Since)
		return nil
	}
	for i, ev := range resp.Events {
		ts := ev.At
		if t, err2 := time.Parse(time.RFC3339Nano, ev.At); err2 == nil {
			ts = t.Local().Format("15:04:05.000")
		}
		phase := ev.Phase
		if phase == "" {
			phase = "-"
		}
		fmt.Printf("[%3d] %s  %-10s  %-12s  %s\n",
			resp.Since+i, ts, ev.Kind, phase, ev.Message)
	}
	return nil
}

// ── cancel ────────────────────────────────────────────────────────────────────

func prReviewsCancelCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <task_id>",
		Short: "Request cancellation of a pending or running PR review task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPRReviewsCancel(gf, args[0])
		},
	}
}

func runPRReviewsCancel(gf *globalFlags, taskID string) error {
	log := buildLogger(gf.logLevel)
	cfg, err := loadConfig(gf.configPath, log)
	if err != nil {
		return err
	}
	base := cfg.PublicGatewayBaseURL()
	url := fmt.Sprintf("%s/api/v1/pr-reviews/%s/cancel", base, taskID)

	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("pr-reviews cancel: build request: %w", err)
	}
	if tok := cfg.Gateway.Token; tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pr-reviews cancel: %w (is opsintelligence running?)", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pr-reviews cancel: gateway returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var out map[string]string
	_ = json.Unmarshal(body, &out)
	fmt.Printf("✓ Cancellation requested for task %s\n", taskID)
	return nil
}

// ── shared helpers ────────────────────────────────────────────────────────────

func prReviewsGET(url, token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w (is opsintelligence running?)", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway returned %s: %s",
			resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func taskIcon(status string) string {
	switch strings.ToLower(status) {
	case "running":
		return "⏳"
	case "pending":
		return "🕐"
	case "completed":
		return "✅"
	case "failed":
		return "❌"
	case "cancelled":
		return "🚫"
	default:
		return "❓"
	}
}

// strconv is used above via strconv.Itoa if needed; keep the import.
var _ = strconv.Itoa // satisfy "imported and not used" for go vet.

// os is used in preflight paths; keep it too so the file compiles standalone.
var _ = os.Getenv // same.
