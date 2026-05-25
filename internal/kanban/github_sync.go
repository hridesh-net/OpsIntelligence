// Package kanban implements GitHub bidirectional issue sync for boards.
package kanban

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	ghclient "github.com/opsintelligence/opsintelligence/internal/devops/github"
)

// GitHubSyncer polls GitHub issues and syncs them to kanban cards.
type GitHubSyncer struct {
	store datastore.Store
	log   *zap.Logger
}

// NewGitHubSyncer creates a syncer.
func NewGitHubSyncer(store datastore.Store, log *zap.Logger) *GitHubSyncer {
	if log == nil {
		log = zap.NewNop()
	}
	return &GitHubSyncer{store: store, log: log}
}

// SyncBoard polls GitHub issues for a board and creates/updates cards.
func (s *GitHubSyncer) SyncBoard(ctx context.Context, boardID, token string) error {
	board, err := s.store.Boards().Get(ctx, boardID)
	if err != nil {
		return fmt.Errorf("board not found: %w", err)
	}
	if board.Mode != "github" || board.RepoURL == "" {
		return fmt.Errorf("board is not in github mode")
	}

	owner, repo := parseRepoURL(board.RepoURL)
	if owner == "" || repo == "" {
		return fmt.Errorf("invalid repo_url: %s", board.RepoURL)
	}

	client := ghclient.New(ghclient.Config{Token: token}, nil)
	issues, err := client.ListIssues(ctx, owner, repo, "open")
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}

	cols, err := s.store.BoardColumns().ListByBoard(ctx, boardID)
	if err != nil || len(cols) == 0 {
		return fmt.Errorf("no columns for board: %w", err)
	}
	defaultCol := cols[0].ID

	existing, err := s.store.BoardCards().List(ctx, datastore.BoardCardFilter{BoardID: boardID, Limit: 1000})
	if err != nil {
		return err
	}
	byIssue := make(map[int]*datastore.BoardCard)
	for i := range existing {
		if existing[i].IssueNumber != nil {
			byIssue[*existing[i].IssueNumber] = &existing[i]
		}
	}

	for _, iss := range issues {
		card, ok := byIssue[iss.Number]
		if ok {
			card.Title = iss.Title
			card.Description = iss.Body
			if err := s.store.BoardCards().Update(ctx, card); err != nil {
				s.log.Warn("github sync: failed to update card", zap.Int("issue", iss.Number), zap.Error(err))
			}
			continue
		}
		cardType := inferCardType(iss.Title, iss.Body)
		card = &datastore.BoardCard{
			ID:          uuid.NewString(),
			BoardID:     boardID,
			ColumnID:    defaultCol,
			IssueNumber: &iss.Number,
			Title:       iss.Title,
			Description: iss.Body,
			CardType:    cardType,
			Priority:    "p2",
			Status:      "queued",
		}
		if err := s.store.BoardCards().Create(ctx, card); err != nil {
			s.log.Warn("github sync: failed to create card", zap.Int("issue", iss.Number), zap.Error(err))
		}
	}

	s.log.Info("github sync complete", zap.String("board", boardID), zap.Int("issues", len(issues)))
	return nil
}

// SyncCardToGitHub pushes card changes back to GitHub as an issue comment.
func (s *GitHubSyncer) SyncCardToGitHub(ctx context.Context, cardID, token, comment string) error {
	card, err := s.store.BoardCards().Get(ctx, cardID)
	if err != nil {
		return err
	}
	if card.IssueNumber == nil {
		return fmt.Errorf("card has no linked issue")
	}
	board, err := s.store.Boards().Get(ctx, card.BoardID)
	if err != nil {
		return err
	}
	owner, repo := parseRepoURL(board.RepoURL)
	if owner == "" || repo == "" {
		return fmt.Errorf("invalid repo_url")
	}

	client := ghclient.New(ghclient.Config{Token: token}, nil)
	_, err = client.CreateIssueComment(ctx, owner, repo, *card.IssueNumber, comment)
	return err
}

func parseRepoURL(u string) (owner, repo string) {
	u = strings.TrimSuffix(u, ".git")
	u = strings.TrimPrefix(u, "https://github.com/")
	u = strings.TrimPrefix(u, "http://github.com/")
	u = strings.TrimPrefix(u, "git@github.com:")
	parts := strings.SplitN(u, "/", 3)
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

func inferCardType(title, body string) string {
	t := strings.ToLower(title + " " + body)
	if strings.Contains(t, "bug") || strings.Contains(t, "fix") || strings.Contains(t, "crash") {
		return "bug"
	}
	if strings.Contains(t, "refactor") {
		return "refactor"
	}
	if strings.Contains(t, "review") {
		return "review"
	}
	if strings.Contains(t, "spike") || strings.Contains(t, "research") {
		return "spike"
	}
	if strings.Contains(t, "chore") || strings.Contains(t, "cleanup") {
		return "chore"
	}
	return "feature"
}
