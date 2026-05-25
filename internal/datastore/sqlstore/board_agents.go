package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type boardAgentRepo struct{ s *Store }

const boardAgentColumns = `id, board_id, name, agent_type, provider_id, config_json, is_default, is_active, created_at`

func (r *boardAgentRepo) Create(ctx context.Context, a *datastore.BoardAgent) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	a.CreatedAt = time.Now().UTC()
	cfg, err := json.Marshal(a.Config)
	if err != nil {
		cfg = []byte("{}")
	}
	_, err = r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO board_agents (`+boardAgentColumns+`) VALUES (?,?,?,?,?,?,?,?,?)`),
		a.ID, a.BoardID, a.Name, a.AgentType, nullable(a.ProviderID), cfg, a.IsDefault, a.IsActive, a.CreatedAt,
	)
	return r.s.scanErr(err)
}

func (r *boardAgentRepo) Get(ctx context.Context, id string) (*datastore.BoardAgent, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+boardAgentColumns+` FROM board_agents WHERE id = ?`), id)
	return scanBoardAgent(row.Scan, r.s.scanErr)
}

func (r *boardAgentRepo) Update(ctx context.Context, a *datastore.BoardAgent) error {
	cfg, err := json.Marshal(a.Config)
	if err != nil {
		cfg = []byte("{}")
	}
	_, err = r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE board_agents SET board_id = ?, name = ?, agent_type = ?, provider_id = ?, config_json = ?, is_default = ?, is_active = ? WHERE id = ?`),
		a.BoardID, a.Name, a.AgentType, nullable(a.ProviderID), cfg, a.IsDefault, a.IsActive, a.ID,
	)
	return r.s.scanErr(err)
}

func (r *boardAgentRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(`DELETE FROM board_agents WHERE id = ?`), id)
	return r.s.scanErr(err)
}

func (r *boardAgentRepo) ListByBoard(ctx context.Context, boardID string) ([]datastore.BoardAgent, error) {
	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(
		`SELECT `+boardAgentColumns+` FROM board_agents WHERE board_id = ? ORDER BY created_at ASC`),
		boardID)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.BoardAgent
	for rows.Next() {
		a, err := scanBoardAgent(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func scanBoardAgent(scan scanFn, mapErr func(error) error) (*datastore.BoardAgent, error) {
	var (
		a          datastore.BoardAgent
		providerID sql.NullString
		cfgJSON    []byte
		isDefault  sql.NullBool
		isActive   sql.NullBool
	)
	if err := scan(
		&a.ID, &a.BoardID, &a.Name, &a.AgentType, &providerID, &cfgJSON, &isDefault, &isActive, &a.CreatedAt,
	); err != nil {
		return nil, mapErr(err)
	}
	a.ProviderID = providerID.String
	if len(cfgJSON) > 0 {
		_ = json.Unmarshal(cfgJSON, &a.Config)
	}
	a.IsDefault = isDefault.Bool
	a.IsActive = isActive.Bool
	return &a, nil
}
