package main

// kanban_cmd.go — `opsintelligence kanban` subcommand. Thin client over
// the gateway's /api/v1/boards endpoints so the operator can drive the
// board from a terminal without leaving the shell.
//
// Commands:
//   opsintelligence kanban boards list
//   opsintelligence kanban boards create --name <name> [--repo <path>]
//   opsintelligence kanban cards list --board <id>
//   opsintelligence kanban cards add --board <id> --title <t> [--description <d>]
//   opsintelligence kanban cards move --card <id> --column <id>
//   opsintelligence kanban dispatch --card <id> [--agent <id>] [--persona <id>] [--slash spec|review|split] [--slash-args <s>]
//   opsintelligence kanban runs list --card <id>
//   opsintelligence kanban runs stop --run <id>
//   opsintelligence kanban autopilot start --card <id> --mode feature-dev|qa [...]
//   opsintelligence kanban autopilot list
//   opsintelligence kanban autopilot stop --session <id>
//   opsintelligence kanban agents list --board <id>
//
// All commands talk to the local gateway via http://127.0.0.1:<port>/api/v1
// with the gateway token from config. The same path the dashboard uses.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/spf13/cobra"
)

func kanbanCmd(gf *globalFlags) *cobra.Command {
	root := &cobra.Command{
		Use:   "kanban",
		Short: "Manage Kanban boards, cards, and agent dispatches",
		Long: `Drive the OpsIntelligence kanban board from the terminal.

Wraps /api/v1/boards endpoints; the daemon must be running.

Common flows:

  # Create a board and a card, then dispatch
  opsintelligence kanban boards create --name "Backlog" --repo .
  opsintelligence kanban cards add --board <id> --title "Add login button"
  opsintelligence kanban dispatch --card <id> --agent claude-code

  # Run autopilot QA loop on a card's worktree
  opsintelligence kanban autopilot start --card <id> --mode qa \
    --check 'go test ./...' --check 'go vet ./...'`,
	}
	root.AddCommand(
		kanbanBoardsCmd(gf),
		kanbanCardsCmd(gf),
		kanbanDispatchCmd(gf),
		kanbanRunsCmd(gf),
		kanbanAutopilotCmd(gf),
		kanbanAgentsCmd(gf),
		kanbanGitHubSyncCmd(gf),
	)
	return root
}

// ── boards ──────────────────────────────────────────────────────────────────

func kanbanBoardsCmd(gf *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "boards", Short: "List or create kanban boards"}
	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List boards",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := kanbanGET(gf, "/api/v1/boards")
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	})
	var name, repoPath string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a new board",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			body := map[string]any{"name": name, "mode": "local"}
			if repoPath != "" {
				body["repo_path"] = repoPath
			}
			out, err := kanbanPOST(gf, "/api/v1/boards", body)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "Board name (required)")
	create.Flags().StringVar(&repoPath, "repo", "", "Absolute path to the git repo this board operates on")
	root.AddCommand(create)
	return root
}

// ── cards ───────────────────────────────────────────────────────────────────

func kanbanCardsCmd(gf *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "cards", Short: "Manage cards"}

	var boardID, columnID, status string
	list := &cobra.Command{
		Use:   "list",
		Short: "List cards on a board",
		RunE: func(cmd *cobra.Command, args []string) error {
			if boardID == "" {
				return fmt.Errorf("--board is required")
			}
			u := fmt.Sprintf("/api/v1/boards/%s/cards", url.PathEscape(boardID))
			q := url.Values{}
			if columnID != "" {
				q.Set("column_id", columnID)
			}
			if status != "" {
				q.Set("status", status)
			}
			if len(q) > 0 {
				u += "?" + q.Encode()
			}
			out, err := kanbanGET(gf, u)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	list.Flags().StringVar(&boardID, "board", "", "Board ID (required)")
	list.Flags().StringVar(&columnID, "column", "", "Filter to one column")
	list.Flags().StringVar(&status, "status", "", "Filter by status")
	root.AddCommand(list)

	var title, description, cardType, priority, effort, assignee string
	add := &cobra.Command{
		Use:   "add",
		Short: "Create a new card",
		RunE: func(cmd *cobra.Command, args []string) error {
			if boardID == "" || title == "" {
				return fmt.Errorf("--board and --title are required")
			}
			body := map[string]any{"title": title}
			if description != "" {
				body["description"] = description
			}
			if cardType != "" {
				body["card_type"] = cardType
			}
			if priority != "" {
				body["priority"] = priority
			}
			if effort != "" {
				body["effort"] = effort
			}
			if assignee != "" {
				body["assignee"] = assignee
			}
			u := fmt.Sprintf("/api/v1/boards/%s/cards", url.PathEscape(boardID))
			out, err := kanbanPOST(gf, u, body)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	add.Flags().StringVar(&boardID, "board", "", "Board ID (required)")
	add.Flags().StringVar(&title, "title", "", "Card title (required)")
	add.Flags().StringVar(&description, "description", "", "Card description")
	add.Flags().StringVar(&cardType, "type", "feature", "Card type (feature / bug / chore)")
	add.Flags().StringVar(&priority, "priority", "p2", "Priority (p0 / p1 / p2 / p3)")
	add.Flags().StringVar(&effort, "effort", "", "Effort estimate (xs / s / m / l / xl)")
	add.Flags().StringVar(&assignee, "assignee", "", "Agent or user assigned to this card")
	root.AddCommand(add)

	var cardID, targetCol string
	move := &cobra.Command{
		Use:   "move",
		Short: "Move a card to a different column",
		RunE: func(cmd *cobra.Command, args []string) error {
			if boardID == "" || cardID == "" || targetCol == "" {
				return fmt.Errorf("--board, --card, --column are all required")
			}
			u := fmt.Sprintf("/api/v1/boards/%s/cards/%s/move", url.PathEscape(boardID), url.PathEscape(cardID))
			out, err := kanbanPOST(gf, u, map[string]any{"column_id": targetCol})
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	move.Flags().StringVar(&boardID, "board", "", "Board ID (required)")
	move.Flags().StringVar(&cardID, "card", "", "Card ID (required)")
	move.Flags().StringVar(&targetCol, "column", "", "Target column ID (required)")
	root.AddCommand(move)
	return root
}

// ── dispatch ────────────────────────────────────────────────────────────────

func kanbanDispatchCmd(gf *globalFlags) *cobra.Command {
	var boardID, cardID, agentID, personaID, model, slash, slashArgs string
	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Dispatch an agent on a card",
		RunE: func(cmd *cobra.Command, args []string) error {
			if boardID == "" || cardID == "" {
				return fmt.Errorf("--board and --card are required")
			}
			body := map[string]any{}
			if agentID != "" {
				body["agent_id"] = agentID
			}
			if personaID != "" {
				body["persona_id"] = personaID
			}
			if model != "" {
				body["model"] = model
			}
			if slash != "" {
				body["slash_command"] = slash
				body["slash_args"] = slashArgs
			}
			u := fmt.Sprintf("/api/v1/boards/%s/cards/%s/dispatch", url.PathEscape(boardID), url.PathEscape(cardID))
			out, err := kanbanPOST(gf, u, body)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	cmd.Flags().StringVar(&boardID, "board", "", "Board ID (required)")
	cmd.Flags().StringVar(&cardID, "card", "", "Card ID (required)")
	cmd.Flags().StringVar(&agentID, "agent", "", "Board agent ID (defaults to card's assignee)")
	cmd.Flags().StringVar(&personaID, "persona", "", "Persona ID to apply as a system-prompt lens")
	cmd.Flags().StringVar(&model, "model", "", "Override the model the agent uses")
	cmd.Flags().StringVar(&slash, "slash", "", "Slash command: spec | review | split")
	cmd.Flags().StringVar(&slashArgs, "slash-args", "", "Argument for the slash command (e.g. subtask count for /split)")
	return cmd
}

// ── runs ───────────────────────────────────────────────────────────────────

func kanbanRunsCmd(gf *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "runs", Short: "Inspect and stop card runs"}
	var runID string
	stop := &cobra.Command{
		Use:   "stop",
		Short: "Stop a running agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runID == "" {
				return fmt.Errorf("--run is required")
			}
			u := fmt.Sprintf("/api/v1/runs/%s/stop", url.PathEscape(runID))
			out, err := kanbanPOST(gf, u, map[string]any{})
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	stop.Flags().StringVar(&runID, "run", "", "Run ID (required)")
	root.AddCommand(stop)

	get := &cobra.Command{
		Use:   "get",
		Short: "Show one run's details, events, and pending decisions",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runID == "" {
				return fmt.Errorf("--run is required")
			}
			out, err := kanbanGET(gf, fmt.Sprintf("/api/v1/runs/%s", url.PathEscape(runID)))
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	get.Flags().StringVar(&runID, "run", "", "Run ID (required)")
	root.AddCommand(get)
	return root
}

// ── autopilot ──────────────────────────────────────────────────────────────

func kanbanAutopilotCmd(gf *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "autopilot", Short: "Start, stop, and inspect autopilot sessions"}

	var cardID, mode, fixAgent string
	var personas []string
	var checks []string
	var parallelism, maxCycles, maxFixAttempts int
	var budget float64
	start := &cobra.Command{
		Use:   "start",
		Short: "Start an autopilot session on a card",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cardID == "" {
				return fmt.Errorf("--card is required")
			}
			body := map[string]any{
				"card_id": cardID,
				"mode":    mode,
			}
			if len(personas) > 0 {
				body["persona_ids"] = personas
			}
			if parallelism > 0 {
				body["parallelism"] = parallelism
			}
			if budget > 0 {
				body["budget_usd"] = budget
			}
			if maxCycles > 0 {
				body["max_cycles"] = maxCycles
			}
			if fixAgent != "" {
				body["fix_agent_id"] = fixAgent
			}
			if maxFixAttempts > 0 {
				body["max_fix_attempts"] = maxFixAttempts
			}
			if len(checks) > 0 {
				cs := make([]map[string]string, 0, len(checks))
				for i, c := range checks {
					cs = append(cs, map[string]string{
						"name": fmt.Sprintf("check-%d", i+1),
						"cmd":  c,
					})
				}
				body["checks"] = cs
			}
			out, err := kanbanPOST(gf, "/api/v1/autopilot", body)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	start.Flags().StringVar(&cardID, "card", "", "Card ID (required)")
	start.Flags().StringVar(&mode, "mode", "feature-dev", "Autopilot mode: feature-dev | qa")
	start.Flags().StringSliceVar(&personas, "persona", nil, "Persona ID to cycle through (repeatable)")
	start.Flags().IntVar(&parallelism, "parallelism", 1, "Concurrent runs per cycle (1-4)")
	start.Flags().Float64Var(&budget, "budget", 0, "Total session cost cap in USD (0 = unlimited)")
	start.Flags().IntVar(&maxCycles, "max-cycles", 0, "Hard cap on cycles (0 = unbounded)")
	start.Flags().StringVar(&fixAgent, "fix-agent", "", "(qa mode) agent to dispatch fix runs against")
	start.Flags().IntVar(&maxFixAttempts, "max-fix-attempts", 3, "(qa mode) consecutive fix attempts per failing check")
	start.Flags().StringSliceVar(&checks, "check", nil, "(qa mode) shell command to run (repeatable)")
	root.AddCommand(start)

	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List autopilot sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := kanbanGET(gf, "/api/v1/autopilot")
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	})

	var sessID string
	stop := &cobra.Command{
		Use:   "stop",
		Short: "Stop a running autopilot session",
		RunE: func(cmd *cobra.Command, args []string) error {
			if sessID == "" {
				return fmt.Errorf("--session is required")
			}
			u := fmt.Sprintf("/api/v1/autopilot/%s/stop", url.PathEscape(sessID))
			out, err := kanbanPOST(gf, u, map[string]any{})
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	stop.Flags().StringVar(&sessID, "session", "", "Autopilot session ID (required)")
	root.AddCommand(stop)
	return root
}

// ── agents ─────────────────────────────────────────────────────────────────

func kanbanAgentsCmd(gf *globalFlags) *cobra.Command {
	root := &cobra.Command{Use: "agents", Short: "Manage board agents"}
	var boardID string
	list := &cobra.Command{
		Use:   "list",
		Short: "List agents registered on a board",
		RunE: func(cmd *cobra.Command, args []string) error {
			if boardID == "" {
				return fmt.Errorf("--board is required")
			}
			u := fmt.Sprintf("/api/v1/boards/%s/agents", url.PathEscape(boardID))
			out, err := kanbanGET(gf, u)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	list.Flags().StringVar(&boardID, "board", "", "Board ID (required)")
	root.AddCommand(list)
	return root
}

// ── github mode sync ────────────────────────────────────────────────────────

func kanbanGitHubSyncCmd(gf *globalFlags) *cobra.Command {
	var boardID string
	cmd := &cobra.Command{
		Use:   "sync-github",
		Short: "Pull GitHub issues into a github-mode board",
		Long: `For a board configured with mode=github, fetch open issues from the linked
repository and upsert them as cards in the first column ("Inbox").
Existing cards (matched by issue_number) have their title and body refreshed.

Requires the daemon to be running and a GitHub token to be available
(set in opsintelligence.yaml under devops.github.token or via
OPSINTELLIGENCE_GITHUB_TOKEN).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if boardID == "" {
				return fmt.Errorf("--board is required")
			}
			u := fmt.Sprintf("/api/v1/boards/%s/github/sync", url.PathEscape(boardID))
			out, err := kanbanPOST(gf, u, map[string]any{})
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	cmd.Flags().StringVar(&boardID, "board", "", "Board ID (required)")
	return cmd
}

// ── HTTP helpers ───────────────────────────────────────────────────────────

func kanbanGET(gf *globalFlags, path string) (string, error) {
	return kanbanRequest(gf, http.MethodGet, path, nil)
}

func kanbanPOST(gf *globalFlags, path string, body any) (string, error) {
	return kanbanRequest(gf, http.MethodPost, path, body)
}

func kanbanRequest(gf *globalFlags, method, p string, body any) (string, error) {
	base, token, err := kanbanGatewayBase(gf)
	if err != nil {
		return "", err
	}
	full := strings.TrimSuffix(base, "/") + path.Clean("/"+p)
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, full, rdr)
	if err != nil {
		return "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("kanban: gateway request failed: %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("kanban: gateway returned %s: %s", resp.Status, strings.TrimSpace(string(out)))
	}
	// Pretty-print JSON when possible.
	var pretty any
	if json.Unmarshal(out, &pretty) == nil {
		b, _ := json.MarshalIndent(pretty, "", "  ")
		return string(b), nil
	}
	return string(out), nil
}

// kanbanGatewayBase resolves the local gateway URL and an auth token to use.
// We prefer 127.0.0.1:<port> over the public Tailscale URL so the kanban CLI
// works even when the daemon's public hostname isn't reachable.
func kanbanGatewayBase(gf *globalFlags) (string, string, error) {
	cfgPath := gf.configPath
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return "", "", fmt.Errorf("kanban: load config: %w", err)
	}
	port := cfg.Gateway.Port
	if port == 0 {
		port = 18790
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	token := strings.TrimSpace(cfg.Gateway.Token)
	// Allow override via env so scripts can target a different daemon.
	if v := os.Getenv("OPSINTELLIGENCE_GATEWAY_URL"); v != "" {
		base = v
	}
	if v := os.Getenv("OPSINTELLIGENCE_GATEWAY_TOKEN"); v != "" {
		token = v
	}
	return base, token, nil
}
