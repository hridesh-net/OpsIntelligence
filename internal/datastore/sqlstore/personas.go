package sqlstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type personaRepo struct{ s *Store }

const personaColumns = `id, name, icon, description, system_prompt, is_builtin, created_by, created_at`

func (r *personaRepo) Create(ctx context.Context, p *datastore.Persona) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	p.CreatedAt = time.Now().UTC()
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO personas (`+personaColumns+`) VALUES (?,?,?,?,?,?,?,?)`),
		p.ID, p.Name, nullable(p.Icon), nullable(p.Description), p.SystemPrompt, p.IsBuiltIn, nullable(p.CreatedBy), p.CreatedAt,
	)
	return r.s.scanErr(err)
}

func (r *personaRepo) Get(ctx context.Context, id string) (*datastore.Persona, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+personaColumns+` FROM personas WHERE id = ?`), id)
	return scanPersona(row.Scan, r.s.scanErr)
}

func (r *personaRepo) Update(ctx context.Context, p *datastore.Persona) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE personas SET name = ?, icon = ?, description = ?, system_prompt = ?, is_builtin = ?, created_by = ? WHERE id = ?`),
		p.Name, nullable(p.Icon), nullable(p.Description), p.SystemPrompt, p.IsBuiltIn, nullable(p.CreatedBy), p.ID,
	)
	return r.s.scanErr(err)
}

func (r *personaRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(`DELETE FROM personas WHERE id = ?`), id)
	return r.s.scanErr(err)
}

func (r *personaRepo) List(ctx context.Context, f datastore.PersonaFilter) ([]datastore.Persona, error) {
	var where []string
	var args []any
	if f.BuiltInOnly {
		where = append(where, `is_builtin = `+r.s.dialect.BoolExpr(true))
	}
	q := `SELECT ` + personaColumns + ` FROM personas`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY created_at DESC`
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q += ` LIMIT ? OFFSET ?`
	args = append(args, limit, f.Offset)

	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(q), args...)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.Persona
	for rows.Next() {
		p, err := scanPersona(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func scanPersona(scan scanFn, mapErr func(error) error) (*datastore.Persona, error) {
	var (
		p         datastore.Persona
		icon      sql.NullString
		desc      sql.NullString
		createdBy sql.NullString
		isBuiltIn sql.NullBool
	)
	if err := scan(
		&p.ID, &p.Name, &icon, &desc, &p.SystemPrompt, &isBuiltIn, &createdBy, &p.CreatedAt,
	); err != nil {
		return nil, mapErr(err)
	}
	p.Icon = icon.String
	p.Description = desc.String
	p.IsBuiltIn = isBuiltIn.Bool
	p.CreatedBy = createdBy.String
	return &p, nil
}
