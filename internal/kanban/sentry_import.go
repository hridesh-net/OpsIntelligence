// Package kanban implements Sentry error-group → card import.
package kanban

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// SentryImporter fetches error groups from Sentry and creates cards.
type SentryImporter struct {
	store datastore.Store
	log   *zap.Logger
}

// NewSentryImporter creates an importer.
func NewSentryImporter(store datastore.Store, log *zap.Logger) *SentryImporter {
	if log == nil {
		log = zap.NewNop()
	}
	return &SentryImporter{store: store, log: log}
}

// SentryIssue is a trimmed Sentry issue (error group).
type SentryIssue struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Culprit     string `json:"culprit"`
	Status      string `json:"status"`
	Level       string `json:"level"`
	Count       int    `json:"count"`
	FirstSeen   string `json:"firstSeen"`
	LastSeen    string `json:"lastSeen"`
	Permalink   string `json:"permalink"`
}

// ImportBoard fetches Sentry issues for a project and creates cards.
func (s *SentryImporter) ImportBoard(ctx context.Context, boardID, dsn, projectSlug string) error {
	cols, err := s.store.BoardColumns().ListByBoard(ctx, boardID)
	if err != nil || len(cols) == 0 {
		return fmt.Errorf("no columns for board: %w", err)
	}
	defaultCol := cols[0].ID

	issues, err := s.fetchIssues(ctx, dsn, projectSlug)
	if err != nil {
		return err
	}

	for _, iss := range issues {
		// Deduplicate by permalink.
		existing, _ := s.store.BoardCards().List(ctx, datastore.BoardCardFilter{
			BoardID: boardID,
			Limit:   1000,
		})
		var found bool
		for _, c := range existing {
			if c.Description == iss.Permalink {
				found = true
				break
			}
		}
		if found {
			continue
		}

		card := &datastore.BoardCard{
			ID:       uuid.NewString(),
			BoardID:  boardID,
			ColumnID: defaultCol,
			Title:    fmt.Sprintf("[%s] %s", iss.Culprit, iss.Title),
			Description: fmt.Sprintf("Sentry: %s\nLevel: %s | Count: %d | First: %s | Last: %s",
				iss.Permalink, iss.Level, iss.Count, iss.FirstSeen, iss.LastSeen),
			CardType: "bug",
			Priority: sentryPriority(iss.Level, iss.Count),
			Status:   "queued",
			Metadata: map[string]any{
				"sentry_issue_id": iss.ID,
				"sentry_permalink": iss.Permalink,
			},
		}
		if err := s.store.BoardCards().Create(ctx, card); err != nil {
			s.log.Warn("sentry import: failed to create card", zap.String("issue", iss.ID), zap.Error(err))
		}
	}

	s.log.Info("sentry import complete", zap.String("board", boardID), zap.Int("issues", len(issues)))
	return nil
}

func (s *SentryImporter) fetchIssues(ctx context.Context, dsn, projectSlug string) ([]SentryIssue, error) {
	// DSN format: https://<key>@<host>/<project>
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	authKey := ""
	if u.User != nil {
		authKey = u.User.String()
		u.User = nil
	}
	base := u.String()
	if base == "" {
		base = "https://sentry.io"
	}

	reqURL := fmt.Sprintf("%s/api/0/projects/%s/issues/?statsPeriod=24h&limit=50", base, projectSlug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+authKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sentry %d", resp.StatusCode)
	}
	var issues []SentryIssue
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		return nil, err
	}
	return issues, nil
}

func sentryPriority(level string, count int) string {
	if level == "fatal" || level == "error" && count > 100 {
		return "p0"
	}
	if level == "error" || count > 10 {
		return "p1"
	}
	return "p2"
}
