// Package sentry imports Sentry issues onto a kanban board, matching
// kanbots.dev's "drop a Sentry error onto a card and dispatch" affordance.
//
// We deliberately keep the integration tiny: no webhooks, no real-time
// stream, just a Pull operation the operator triggers when they want
// to backfill the board. The Sentry side is read-only — we don't write
// resolution status back (yet).
//
// Authentication is via a Sentry "Auth Token" (Project → Settings →
// Auth Tokens with scope `event:read`, `project:read`). Pass through
// the gateway config (`devops.sentry.token`) or env
// `OPSINTELLIGENCE_SENTRY_TOKEN`.
package sentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// Client is the minimal subset of the Sentry REST API we need.
type Client struct {
	BaseURL string // default https://sentry.io
	Token   string
	HTTP    *http.Client
}

// NewClient returns a configured Sentry client.
func NewClient(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = "https://sentry.io"
	}
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Issue is a trimmed Sentry issue payload.
type Issue struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Culprit   string `json:"culprit"`
	Level     string `json:"level"`
	Status    string `json:"status"`
	Permalink string `json:"permalink"`
	Count     string `json:"count"`
	Type      string `json:"type"`
	Metadata  struct {
		Value string `json:"value"`
	} `json:"metadata"`
}

// ListIssues fetches the top unresolved issues for the given org/project.
// `query` is a Sentry search expression (e.g. `is:unresolved`).
func (c *Client) ListIssues(ctx context.Context, org, project, query string) ([]Issue, error) {
	if query == "" {
		query = "is:unresolved"
	}
	u, err := url.Parse(fmt.Sprintf("%s/api/0/projects/%s/%s/issues/", c.BaseURL, org, project))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("limit", "100")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sentry: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("sentry %s: %s", resp.Status, string(body))
	}
	var out []Issue
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("sentry decode: %w", err)
	}
	return out, nil
}

// Importer turns Sentry issues into kanban cards on a target board.
type Importer struct {
	Store  datastore.Store
	Client *Client
}

// New constructs an Importer.
func New(store datastore.Store, client *Client) *Importer {
	return &Importer{Store: store, Client: client}
}

// Import pulls Sentry issues and upserts them as cards in the board's
// first column. Cards are matched by metadata["sentry_id"] so re-importing
// is idempotent.
func (i *Importer) Import(ctx context.Context, boardID, org, project, query string) (added, updated int, err error) {
	if i == nil || i.Client == nil {
		return 0, 0, errors.New("sentry import: client not configured")
	}
	cols, err := i.Store.BoardColumns().ListByBoard(ctx, boardID)
	if err != nil {
		return 0, 0, err
	}
	if len(cols) == 0 {
		return 0, 0, fmt.Errorf("sentry import: board %q has no columns", boardID)
	}
	inboxID := cols[0].ID

	issues, err := i.Client.ListIssues(ctx, org, project, query)
	if err != nil {
		return 0, 0, err
	}

	// Index existing cards by sentry_id stored in metadata so re-running
	// the importer doesn't create duplicates.
	cards, err := i.Store.BoardCards().List(ctx, datastore.BoardCardFilter{BoardID: boardID, Limit: 1000})
	if err != nil {
		return 0, 0, err
	}
	bySentry := map[string]*datastore.BoardCard{}
	for j := range cards {
		c := &cards[j]
		if c.Metadata != nil {
			if v, ok := c.Metadata["sentry_id"].(string); ok {
				bySentry[v] = c
			}
		}
	}

	for _, iss := range issues {
		title := truncTitle(iss.Title, 120)
		desc := buildDescription(iss)
		if cur, ok := bySentry[iss.ID]; ok {
			cur.Title = title
			cur.Description = desc
			cur.UpdatedAt = time.Now().UTC()
			if err := i.Store.BoardCards().Update(ctx, cur); err == nil {
				updated++
			}
			continue
		}
		card := &datastore.BoardCard{
			ID:          uuid.NewString(),
			BoardID:     boardID,
			ColumnID:    inboxID,
			Title:       title,
			Description: desc,
			CardType:    "bug",
			Priority:    priorityFromLevel(iss.Level),
			Status:      "todo",
			Metadata: map[string]any{
				"sentry_id":   iss.ID,
				"sentry_url":  iss.Permalink,
				"sentry_lvl":  iss.Level,
				"sentry_seen": iss.Count,
			},
		}
		if err := i.Store.BoardCards().Create(ctx, card); err == nil {
			added++
		}
	}
	return added, updated, nil
}

func buildDescription(iss Issue) string {
	out := fmt.Sprintf("**Sentry issue %s** (%s)\n\n", iss.ID, iss.Level)
	if iss.Culprit != "" {
		out += "Culprit: `" + iss.Culprit + "`\n"
	}
	if iss.Metadata.Value != "" {
		out += "\n```\n" + iss.Metadata.Value + "\n```\n"
	}
	if iss.Permalink != "" {
		out += "\n[Open in Sentry](" + iss.Permalink + ")\n"
	}
	return out
}

func priorityFromLevel(level string) string {
	switch level {
	case "fatal", "error":
		return "p1"
	case "warning":
		return "p2"
	default:
		return "p3"
	}
}

func truncTitle(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
