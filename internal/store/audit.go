package store

import (
	"context"
	"fmt"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// AppendAudit adds an append-only audit record (SEC-TRN-07).
func (s *Store) AppendAudit(ctx context.Context, e domain.AuditEntry) error {
	const q = `INSERT INTO audit_log (at, action, path, size, plan_id, result, detail)
	           VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, q, formatTime(e.At), e.Action, e.Path, e.Size,
		e.PlanID, e.Result, e.Detail)
	if err != nil {
		return fmt.Errorf("store: append audit: %w", err)
	}
	return nil
}

// AuditLog returns the most recent audit records (newest first). A limit <= 0
// returns every record.
func (s *Store) AuditLog(ctx context.Context, limit int) ([]domain.AuditEntry, error) {
	q := `SELECT id, at, action, path, size, plan_id, result, detail
	      FROM audit_log ORDER BY id DESC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query audit: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.AuditEntry
	for rows.Next() {
		var (
			e  domain.AuditEntry
			at string
		)
		if err := rows.Scan(&e.ID, &at, &e.Action, &e.Path, &e.Size,
			&e.PlanID, &e.Result, &e.Detail); err != nil {
			return nil, fmt.Errorf("store: scan audit row: %w", err)
		}
		e.At = parseTime(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
