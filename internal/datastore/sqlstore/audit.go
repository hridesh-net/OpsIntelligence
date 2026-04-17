package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type auditRepo struct{ s *Store }

func (r *auditRepo) Append(ctx context.Context, e *datastore.AuditEntry) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.ActorType == "" {
		e.ActorType = datastore.ActorSystem
	}
	var metadataJSON sql.NullString
	if len(e.Metadata) > 0 {
		b, err := json.Marshal(e.Metadata)
		if err != nil {
			return err
		}
		metadataJSON.String = string(b)
		metadataJSON.Valid = true
	}
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`INSERT INTO audit_log(
            timestamp, actor_type, actor_id, action,
            resource_type, resource_id, metadata_json,
            remote_addr, user_agent, success, error_message
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?)`),
		e.Timestamp, string(e.ActorType), nullable(e.ActorID), e.Action,
		nullable(e.ResourceType), nullable(e.ResourceID), metadataJSON,
		nullable(e.RemoteAddr), nullable(e.UserAgent), e.Success, nullable(e.ErrorMessage),
	)
	return r.s.scanErr(err)
}

func (r *auditRepo) List(ctx context.Context, f datastore.AuditFilter) ([]datastore.AuditEntry, error) {
	q, args := buildAuditFilter(`SELECT id, timestamp, actor_type, actor_id, action,
        resource_type, resource_id, metadata_json, remote_addr, user_agent,
        success, error_message FROM audit_log`, f, true)
	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(q), args...)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.AuditEntry
	for rows.Next() {
		var (
			e        datastore.AuditEntry
			actorID  sql.NullString
			resType  sql.NullString
			resID    sql.NullString
			meta     sql.NullString
			addr     sql.NullString
			ua       sql.NullString
			errMsg   sql.NullString
			actorTyp string
		)
		if err := rows.Scan(
			&e.ID, &e.Timestamp, &actorTyp, &actorID, &e.Action,
			&resType, &resID, &meta, &addr, &ua, &e.Success, &errMsg,
		); err != nil {
			return nil, r.s.scanErr(err)
		}
		e.ActorType = datastore.ActorType(actorTyp)
		e.ActorID = actorID.String
		e.ResourceType = resType.String
		e.ResourceID = resID.String
		e.RemoteAddr = addr.String
		e.UserAgent = ua.String
		e.ErrorMessage = errMsg.String
		if meta.Valid && meta.String != "" {
			_ = json.Unmarshal([]byte(meta.String), &e.Metadata)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *auditRepo) Count(ctx context.Context, f datastore.AuditFilter) (int, error) {
	q, args := buildAuditFilter(`SELECT COUNT(*) FROM audit_log`, f, false)
	var n int
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(q), args...)
	if err := row.Scan(&n); err != nil {
		return 0, r.s.scanErr(err)
	}
	return n, nil
}

// buildAuditFilter composes a WHERE clause using `?` placeholders (the
// caller passes the result through Store.rebind). When paged == true,
// ORDER BY + LIMIT/OFFSET are appended; Count() sets it to false.
func buildAuditFilter(base string, f datastore.AuditFilter, paged bool) (string, []any) {
	var (
		where []string
		args  []any
	)
	if f.Since != nil {
		where = append(where, `timestamp >= ?`)
		args = append(args, *f.Since)
	}
	if f.Until != nil {
		where = append(where, `timestamp < ?`)
		args = append(args, *f.Until)
	}
	if f.Actor != "" {
		where = append(where, `actor_id = ?`)
		args = append(args, f.Actor)
	}
	if f.ActorType != "" {
		where = append(where, `actor_type = ?`)
		args = append(args, string(f.ActorType))
	}
	if f.Action != "" {
		where = append(where, `action LIKE ?`)
		args = append(args, f.Action+`%`)
	}
	if f.Resource != "" {
		where = append(where, `resource_type = ?`)
		args = append(args, f.Resource)
	}
	if f.Success != nil {
		where = append(where, `success = ?`)
		args = append(args, *f.Success)
	}
	q := base
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	if paged {
		q += ` ORDER BY timestamp DESC`
		limit := f.Limit
		if limit <= 0 {
			limit = 100
		}
		q += ` LIMIT ? OFFSET ?`
		args = append(args, limit, f.Offset)
	}
	return q, args
}
