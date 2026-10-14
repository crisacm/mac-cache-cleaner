package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// CreatePlan persists an immutable plan and its items in one transaction.
func (s *Store) CreatePlan(ctx context.Context, p domain.Plan) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		const q = `INSERT INTO plans (plan_id, hash, created_at, expires_at, status)
		           VALUES (?, ?, ?, ?, ?)`
		if _, err := tx.ExecContext(ctx, q, p.PlanID, p.Hash,
			formatTime(p.CreatedAt), formatTime(p.ExpiresAt), string(p.Status)); err != nil {
			return fmt.Errorf("store: create plan: %w", err)
		}
		const qi = `INSERT INTO plan_items (plan_id, entry_id, path, dev, inode, size, mtime, data_class)
		            VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
		stmt, err := tx.PrepareContext(ctx, qi)
		if err != nil {
			return fmt.Errorf("store: prepare plan items: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for _, it := range p.Items {
			if _, err := stmt.ExecContext(ctx, p.PlanID, it.EntryID, it.Path,
				devToInt64(it.Dev), devToInt64(it.Inode), it.Size, formatTime(it.MTime),
				string(it.DataClass)); err != nil {
				return fmt.Errorf("store: insert plan item %s: %w", it.Path, err)
			}
		}
		return nil
	})
}

// GetPlan returns a plan (without items) by ID.
func (s *Store) GetPlan(ctx context.Context, planID string) (domain.Plan, error) {
	const q = `SELECT plan_id, hash, created_at, expires_at, status FROM plans WHERE plan_id = ?`
	var (
		p       domain.Plan
		created string
		expires string
		status  string
	)
	err := s.db.QueryRowContext(ctx, q, planID).Scan(&p.PlanID, &p.Hash, &created, &expires, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Plan{}, fmt.Errorf("store: plan %s: %w", planID, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Plan{}, fmt.Errorf("store: get plan: %w", err)
	}
	p.CreatedAt = parseTime(created)
	p.ExpiresAt = parseTime(expires)
	p.Status = domain.PlanStatus(status)
	return p, nil
}

// PlanItems returns the items of a plan.
func (s *Store) PlanItems(ctx context.Context, planID string) ([]domain.PlanItem, error) {
	const q = `SELECT plan_id, entry_id, path, dev, inode, size, mtime, data_class
	           FROM plan_items WHERE plan_id = ? ORDER BY path`
	rows, err := s.db.QueryContext(ctx, q, planID)
	if err != nil {
		return nil, fmt.Errorf("store: query plan items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.PlanItem
	for rows.Next() {
		var (
			it    domain.PlanItem
			dev   int64
			inode int64
			mtime string
			class string
		)
		if err := rows.Scan(&it.PlanID, &it.EntryID, &it.Path, &dev, &inode,
			&it.Size, &mtime, &class); err != nil {
			return nil, fmt.Errorf("store: scan plan item row: %w", err)
		}
		it.Dev = int64ToDev(dev)
		it.Inode = int64ToDev(inode)
		it.MTime = parseTime(mtime)
		it.DataClass = domain.DataClass(class)
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdatePlanStatus changes a plan's status.
func (s *Store) UpdatePlanStatus(ctx context.Context, planID string, status domain.PlanStatus) error {
	const q = `UPDATE plans SET status = ? WHERE plan_id = ?`
	res, err := s.db.ExecContext(ctx, q, string(status), planID)
	if err != nil {
		return fmt.Errorf("store: update plan status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: plan %s: %w", planID, domain.ErrNotFound)
	}
	return nil
}

// ListPlans returns plans ordered by creation time (newest first).
func (s *Store) ListPlans(ctx context.Context) ([]domain.Plan, error) {
	const q = `SELECT plan_id, hash, created_at, expires_at, status
	           FROM plans ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: query plans: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Plan
	for rows.Next() {
		var (
			p       domain.Plan
			created string
			expires string
			status  string
		)
		if err := rows.Scan(&p.PlanID, &p.Hash, &created, &expires, &status); err != nil {
			return nil, fmt.Errorf("store: scan plan row: %w", err)
		}
		p.CreatedAt = parseTime(created)
		p.ExpiresAt = parseTime(expires)
		p.Status = domain.PlanStatus(status)
		out = append(out, p)
	}
	return out, rows.Err()
}
