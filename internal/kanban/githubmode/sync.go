// Package githubmode bridges a kanban board's cards to a GitHub
// repository's issues. Enabled per-board by setting Board.Mode = "github"
// and Board.Config["github"] = {"owner": "...", "repo": "...", "token": "..."}
// (token is optional — falls back to OPSINTELLIGENCE_GITHUB_TOKEN, then to
// the gateway-level GitHub App credentials).
//
// The bridge is one-way-with-feedback:
//
//   GitHub issue → local card        (Sync, called on demand + on board open)
//   local card  → GitHub issue:
//     * card created in GitHub-mode board     → open new GH issue
//     * card moved to a different column      → set GH labels to {column-name}
//     * card moved to the "Done" column       → close the GH issue
//     * card dispatch run starts / completes  → post a comment on the issue
//
// We deliberately don't try to write a generic two-way sync engine; this
// repo is small enough that the kanban board is the source of truth for
// column structure / dispatch state, and GitHub is the source of truth for
// "is this issue still relevant?". Sync() is run on demand and on the
// per-board status tick.
package githubmode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/devops/github"
)

// Sync owns the bridge. One instance per gateway; safe to use across boards.
type Sync struct {
	Store  datastore.Store
	Client *github.Client
}

// New constructs a Sync. Pass nil for client to disable the bridge (Sync()
// then becomes a no-op so callers don't need a conditional).
func New(store datastore.Store, client *github.Client) *Sync {
	return &Sync{Store: store, Client: client}
}

// BoardConfig carries the per-board GitHub coordinates pulled out of
// Board.Config["github"].
type BoardConfig struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	// Token, when non-empty, overrides the gateway's default token. Useful
	// when a board belongs to an org that the gateway's App isn't installed
	// on. We DON'T persist a per-board token without operator action; the
	// expected workflow is to set it manually via the board API.
	Token string `json:"token,omitempty"`
}

// parseBoardConfig returns the board's GitHub coordinates, or an error
// when the board isn't configured for GitHub mode.
func parseBoardConfig(board *datastore.Board) (*BoardConfig, error) {
	if board == nil {
		return nil, errors.New("githubmode: nil board")
	}
	if board.Mode != "github" {
		return nil, fmt.Errorf("githubmode: board %q mode is %q, not github", board.ID, board.Mode)
	}
	raw, ok := board.Config["github"]
	if !ok {
		return nil, fmt.Errorf("githubmode: board %q has mode=github but no config.github block", board.ID)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("githubmode: re-marshal config: %w", err)
	}
	var cfg BoardConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("githubmode: decode config: %w", err)
	}
	if cfg.Owner == "" || cfg.Repo == "" {
		return nil, fmt.Errorf("githubmode: config.github.owner and .repo are required")
	}
	if cfg.Token == "" {
		// Fall back to env so the operator doesn't have to embed PATs in
		// the board's config.json.
		cfg.Token = os.Getenv("OPSINTELLIGENCE_GITHUB_TOKEN")
	}
	return &cfg, nil
}

// PullIssues lists open issues on the configured repo and upserts them
// as cards on the board. The first column is treated as the inbox.
func (s *Sync) PullIssues(ctx context.Context, boardID string) (added, updated int, err error) {
	if s == nil || s.Client == nil {
		return 0, 0, errors.New("githubmode: sync not configured")
	}
	board, err := s.Store.Boards().Get(ctx, boardID)
	if err != nil {
		return 0, 0, err
	}
	cfg, err := parseBoardConfig(board)
	if err != nil {
		return 0, 0, err
	}

	cols, err := s.Store.BoardColumns().ListByBoard(ctx, boardID)
	if err != nil {
		return 0, 0, err
	}
	if len(cols) == 0 {
		return 0, 0, fmt.Errorf("githubmode: board %q has no columns", boardID)
	}
	inboxID := cols[0].ID

	issues, err := s.Client.ListIssues(ctx, cfg.Owner, cfg.Repo, "open")
	if err != nil {
		return 0, 0, fmt.Errorf("githubmode: list issues: %w", err)
	}

	// Build an index of existing cards by issue_number for cheap upsert.
	existing, err := s.Store.BoardCards().List(ctx, datastore.BoardCardFilter{BoardID: boardID, Limit: 1000})
	if err != nil {
		return 0, 0, err
	}
	byNumber := map[int]*datastore.BoardCard{}
	for i := range existing {
		c := existing[i]
		if c.IssueNumber != nil {
			byNumber[*c.IssueNumber] = &existing[i]
		}
	}

	for _, iss := range issues {
		if cur, ok := byNumber[iss.Number]; ok {
			// Update title / description from the upstream issue. Don't
			// touch column — the operator may have moved the card.
			cur.Title = iss.Title
			cur.Description = iss.Body
			cur.UpdatedAt = time.Now().UTC()
			if err := s.Store.BoardCards().Update(ctx, cur); err == nil {
				updated++
			}
			continue
		}
		num := iss.Number
		card := &datastore.BoardCard{
			ID:          uuid.NewString(),
			BoardID:     boardID,
			ColumnID:    inboxID,
			IssueNumber: &num,
			Title:       iss.Title,
			Description: iss.Body,
			CardType:    "issue",
			Priority:    "p2",
			Status:      "todo",
		}
		if err := s.Store.BoardCards().Create(ctx, card); err == nil {
			added++
		}
	}
	return added, updated, nil
}

// PushCardCreated mirrors a newly-created kanban card to a new GitHub
// issue when the board is in GitHub mode. The card is updated in place
// to carry the issue number returned by GitHub.
func (s *Sync) PushCardCreated(ctx context.Context, card *datastore.BoardCard) error {
	if s == nil || s.Client == nil || card == nil {
		return nil
	}
	board, err := s.Store.Boards().Get(ctx, card.BoardID)
	if err != nil {
		return err
	}
	cfg, err := parseBoardConfig(board)
	if err != nil {
		// Not a GitHub-mode board — silently skip so this is safe to call
		// unconditionally from the card-create path.
		return nil
	}
	if card.IssueNumber != nil {
		// Card already has an issue number (came from a previous Pull or
		// was created with explicit IssueNumber).
		return nil
	}
	issue, err := s.Client.CreateIssue(ctx, cfg.Owner, cfg.Repo, card.Title, card.Description, cardLabels(card))
	if err != nil {
		return err
	}
	num := issue.Number
	card.IssueNumber = &num
	card.UpdatedAt = time.Now().UTC()
	return s.Store.BoardCards().Update(ctx, card)
}

// PushCardMoved updates the GitHub issue's labels (and optionally state)
// to reflect a column change. No-op for non-GitHub boards.
func (s *Sync) PushCardMoved(ctx context.Context, cardID string, newColumnID string) error {
	if s == nil || s.Client == nil {
		return nil
	}
	card, err := s.Store.BoardCards().Get(ctx, cardID)
	if err != nil || card == nil {
		return err
	}
	if card.IssueNumber == nil {
		return nil // local-only card
	}
	board, err := s.Store.Boards().Get(ctx, card.BoardID)
	if err != nil {
		return err
	}
	cfg, err := parseBoardConfig(board)
	if err != nil {
		return nil
	}
	cols, err := s.Store.BoardColumns().ListByBoard(ctx, card.BoardID)
	if err != nil {
		return err
	}
	var colName string
	for _, c := range cols {
		if c.ID == newColumnID {
			colName = c.Name
			break
		}
	}
	if colName == "" {
		return fmt.Errorf("githubmode: unknown column %q", newColumnID)
	}

	labels := append([]string{"kanban/" + slug(colName)}, cardLabels(card)...)
	if err := s.Client.SetIssueLabels(ctx, cfg.Owner, cfg.Repo, *card.IssueNumber, labels); err != nil {
		return err
	}
	if isDoneColumn(colName) {
		if err := s.Client.CloseIssue(ctx, cfg.Owner, cfg.Repo, *card.IssueNumber); err != nil {
			return err
		}
	}
	return nil
}

// PostRunComment writes a Markdown summary of a card-run to the GitHub
// issue so reviewers see what the agent did without leaving GitHub.
func (s *Sync) PostRunComment(ctx context.Context, run *datastore.CardRun) error {
	if s == nil || s.Client == nil || run == nil {
		return nil
	}
	card, err := s.Store.BoardCards().Get(ctx, run.CardID)
	if err != nil || card == nil || card.IssueNumber == nil {
		return nil
	}
	board, err := s.Store.Boards().Get(ctx, card.BoardID)
	if err != nil {
		return err
	}
	cfg, err := parseBoardConfig(board)
	if err != nil {
		return nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**OpsIntelligence dispatch — %s** (%s)\n\n", run.Status, run.AgentType))
	if run.Model != "" {
		sb.WriteString(fmt.Sprintf("- model: `%s`\n", run.Model))
	}
	if run.Branch != "" {
		sb.WriteString(fmt.Sprintf("- branch: `%s`\n", run.Branch))
	}
	if run.ElapsedMs > 0 {
		sb.WriteString(fmt.Sprintf("- elapsed: %dms\n", run.ElapsedMs))
	}
	if run.CostUSD > 0 {
		sb.WriteString(fmt.Sprintf("- cost: $%.4f (in: %d / out: %d tokens)\n", run.CostUSD, run.TokenIn, run.TokenOut))
	}
	if run.Error != "" {
		sb.WriteString(fmt.Sprintf("\nError:\n```\n%s\n```\n", run.Error))
	}
	_, err = s.Client.CreateIssueComment(ctx, cfg.Owner, cfg.Repo, *card.IssueNumber, sb.String())
	return err
}

// cardLabels returns the GitHub labels we want to set on the issue based
// on the card's intrinsic attributes (type / priority). Doesn't include
// the column-derived label — that's added by the mover.
func cardLabels(card *datastore.BoardCard) []string {
	out := []string{}
	if card.CardType != "" {
		out = append(out, "type/"+card.CardType)
	}
	if card.Priority != "" {
		out = append(out, "priority/"+card.Priority)
	}
	return out
}

func isDoneColumn(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "done", "completed", "closed", "shipped":
		return true
	}
	return false
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

// Ensure the package isn't dropped by the linker when no other package
// references it yet — useful while wiring up the gateway.
var _ = http.MethodGet
