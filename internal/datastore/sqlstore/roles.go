package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type roleRepo struct{ s *Store }

const roleColumns = `id, name, description, is_builtin, created_at`

func (r *roleRepo) Create(ctx context.Context, role *datastore.Role) error {
	if role.CreatedAt.IsZero() {
		role.CreatedAt = time.Now().UTC()
	}
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`INSERT INTO roles(id, name, description, is_builtin, created_at)
            VALUES (?, ?, ?, ?, ?)`),
		role.ID, role.Name, nullable(role.Description), role.IsBuiltIn, role.CreatedAt,
	)
	return r.s.scanErr(err)
}

func (r *roleRepo) Get(ctx context.Context, id string) (*datastore.Role, error) {
	row := r.s.db.QueryRowContext(ctx,
		r.s.rebind(`SELECT `+roleColumns+` FROM roles WHERE id = ?`), id)
	return scanRole(row.Scan, r.s.scanErr)
}

func (r *roleRepo) GetByName(ctx context.Context, name string) (*datastore.Role, error) {
	row := r.s.db.QueryRowContext(ctx,
		r.s.rebind(`SELECT `+roleColumns+` FROM roles WHERE name = ?`), name)
	return scanRole(row.Scan, r.s.scanErr)
}

func (r *roleRepo) List(ctx context.Context) ([]datastore.Role, error) {
	rows, err := r.s.db.QueryContext(ctx,
		`SELECT `+roleColumns+` FROM roles ORDER BY is_builtin DESC, name ASC`)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.Role
	for rows.Next() {
		role, err := scanRole(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *role)
	}
	return out, rows.Err()
}

func (r *roleRepo) Update(ctx context.Context, role *datastore.Role) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`UPDATE roles SET name = ?, description = ? WHERE id = ?`),
		role.Name, nullable(role.Description), role.ID)
	return r.s.scanErr(err)
}

func (r *roleRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`DELETE FROM roles WHERE id = ? AND is_builtin = ?`), id, false)
	return r.s.scanErr(err)
}

func (r *roleRepo) SetPermissions(ctx context.Context, roleID string, perms []datastore.Permission) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return r.s.scanErr(err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		r.s.rebind(`DELETE FROM role_permissions WHERE role_id = ?`), roleID); err != nil {
		return r.s.scanErr(err)
	}
	stmt, err := tx.PrepareContext(ctx,
		r.s.rebind(`INSERT INTO role_permissions(role_id, permission_key) VALUES (?, ?)`))
	if err != nil {
		return r.s.scanErr(err)
	}
	defer stmt.Close()

	for _, p := range perms {
		if string(p) == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, roleID, string(p)); err != nil {
			return r.s.scanErr(err)
		}
	}
	return tx.Commit()
}

func (r *roleRepo) ListPermissions(ctx context.Context, roleID string) ([]datastore.Permission, error) {
	rows, err := r.s.db.QueryContext(ctx,
		r.s.rebind(`SELECT permission_key FROM role_permissions WHERE role_id = ? ORDER BY permission_key`),
		roleID)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.Permission
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, datastore.Permission(k))
	}
	return out, rows.Err()
}

func (r *roleRepo) AssignToUser(ctx context.Context, userID, roleID string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`INSERT INTO user_roles(user_id, role_id, assigned_at) VALUES (?, ?, ?)`),
		userID, roleID, time.Now().UTC())
	return r.s.scanErr(err)
}

func (r *roleRepo) RevokeFromUser(ctx context.Context, userID, roleID string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`DELETE FROM user_roles WHERE user_id = ? AND role_id = ?`),
		userID, roleID)
	return r.s.scanErr(err)
}

func (r *roleRepo) ListRolesForUser(ctx context.Context, userID string) ([]datastore.Role, error) {
	rows, err := r.s.db.QueryContext(ctx,
		r.s.rebind(`SELECT `+prefixCols("r", roleColumns)+`
            FROM roles r
            INNER JOIN user_roles ur ON ur.role_id = r.id
            WHERE ur.user_id = ?
            ORDER BY r.name`),
		userID)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.Role
	for rows.Next() {
		role, err := scanRole(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *role)
	}
	return out, rows.Err()
}

func (r *roleRepo) ListPermissionsForUser(ctx context.Context, userID string) ([]datastore.Permission, error) {
	rows, err := r.s.db.QueryContext(ctx,
		r.s.rebind(`SELECT DISTINCT rp.permission_key
            FROM role_permissions rp
            INNER JOIN user_roles ur ON ur.role_id = rp.role_id
            WHERE ur.user_id = ?
            ORDER BY rp.permission_key`),
		userID)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.Permission
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, datastore.Permission(k))
	}
	return out, rows.Err()
}

// ─── helpers ────────────────────────────────────────────────────────

func scanRole(scan scanFn, mapErr func(error) error) (*datastore.Role, error) {
	var (
		role        datastore.Role
		description sql.NullString
	)
	if err := scan(&role.ID, &role.Name, &description, &role.IsBuiltIn, &role.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	role.Description = description.String
	return &role, nil
}

// prefixCols turns "id, name, …" into "r.id, r.name, …". Useful for
// JOINs that would otherwise need the alias spelled out twice.
func prefixCols(alias, cols string) string {
	out := ""
	for _, part := range splitCSV(cols) {
		if out != "" {
			out += ", "
		}
		out += alias + "." + part
	}
	return out
}

func splitCSV(s string) []string {
	var (
		out  []string
		curr string
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ',' {
			if t := trimSpace(curr); t != "" {
				out = append(out, t)
			}
			curr = ""
			continue
		}
		curr += string(c)
	}
	if t := trimSpace(curr); t != "" {
		out = append(out, t)
	}
	return out
}

func trimSpace(s string) string {
	lo, hi := 0, len(s)
	for lo < hi && (s[lo] == ' ' || s[lo] == '\n' || s[lo] == '\t') {
		lo++
	}
	for hi > lo && (s[hi-1] == ' ' || s[hi-1] == '\n' || s[hi-1] == '\t') {
		hi--
	}
	return s[lo:hi]
}
