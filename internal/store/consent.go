package store

import (
	"context"
	"fmt"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// GrantConsent records (or re-activates) consent for a root. Consent is per
// root and revocable (SEC-ACC-07).
func (s *Store) GrantConsent(ctx context.Context, c domain.Consent) error {
	const q = `INSERT INTO consents (root, tier, granted_at, revoked_at)
	           VALUES (?, ?, ?, NULL)
	           ON CONFLICT (root) DO UPDATE SET
	               tier = excluded.tier,
	               granted_at = excluded.granted_at,
	               revoked_at = NULL`
	_, err := s.db.ExecContext(ctx, q, c.Root, int(c.Tier), formatTime(c.GrantedAt))
	if err != nil {
		return fmt.Errorf("store: grant consent %s: %w", c.Root, err)
	}
	return nil
}

// RevokeConsent marks the consent for a root as revoked.
func (s *Store) RevokeConsent(ctx context.Context, root string, at time.Time) error {
	const q = `UPDATE consents SET revoked_at = ? WHERE root = ?`
	res, err := s.db.ExecContext(ctx, q, formatTime(at), root)
	if err != nil {
		return fmt.Errorf("store: revoke consent %s: %w", root, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: revoke consent %s: %w", root, domain.ErrNotFound)
	}
	return nil
}

// Consents returns every recorded consent, revoked or not.
func (s *Store) Consents(ctx context.Context) ([]domain.Consent, error) {
	const q = `SELECT root, tier, granted_at, revoked_at FROM consents ORDER BY root`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: query consents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Consent
	for rows.Next() {
		var (
			c         domain.Consent
			tier      int
			granted   string
			revokedAt *string
		)
		if err := rows.Scan(&c.Root, &tier, &granted, &revokedAt); err != nil {
			return nil, fmt.Errorf("store: scan consent row: %w", err)
		}
		c.Tier = domain.Tier(tier)
		c.GrantedAt = parseTime(granted)
		if revokedAt != nil {
			c.RevokedAt = parseTime(*revokedAt)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ActiveConsent returns the consent in force for root at now, if any.
func (s *Store) ActiveConsent(ctx context.Context, root string, now time.Time) (domain.Consent, bool, error) {
	consents, err := s.Consents(ctx)
	if err != nil {
		return domain.Consent{}, false, err
	}
	for _, c := range consents {
		if c.Root == root && c.Active(now) {
			return c, true, nil
		}
	}
	return domain.Consent{}, false, nil
}
