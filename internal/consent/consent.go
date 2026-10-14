// Package consent manages per-root data-access consent.
//
// Consent is granted only interactively on a TTY, is per root, and is recorded
// so it can be revoked (SEC-ACC-07). No flag or shortcut grants consent. The
// package decides nothing about access itself: it asks internal/policy for the
// tier and records the grant.
package consent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
)

// ErrNotNeeded means the root is a default root and needs no consent.
var ErrNotNeeded = errors.New("consent not needed for a default root")

// Repo records consents (implemented by *store.Store).
type Repo interface {
	Consents(ctx context.Context) ([]domain.Consent, error)
	GrantConsent(ctx context.Context, c domain.Consent) error
	RevokeConsent(ctx context.Context, root string, at time.Time) error
}

// Policy classifies roots (implemented by *policy.Policy).
type Policy interface {
	RootVerdict(ctx context.Context, root string) (policy.Verdict, error)
	Home() string
}

// Service manages consents.
type Service struct {
	repo        Repo
	policy      Policy
	interactive bool
	now         func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithClock overrides the clock.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// New builds a Service. interactive must be true only when a real terminal is
// attached; otherwise granting is refused (SEC-ACC-07).
func New(repo Repo, p Policy, interactive bool, opts ...Option) *Service {
	s := &Service{repo: repo, policy: p, interactive: interactive, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// List returns every recorded consent.
func (s *Service) List(ctx context.Context) ([]domain.Consent, error) {
	return s.repo.Consents(ctx)
}

// Verdict returns policy's decision for a root, for display before granting.
func (s *Service) Verdict(ctx context.Context, root string) (policy.Verdict, error) {
	resolved, err := s.resolve(root)
	if err != nil {
		return policy.Verdict{}, err
	}
	return s.policy.RootVerdict(ctx, resolved)
}

// Grant records consent for a root after an interactive confirmation has
// already happened. It refuses when not interactive, when the root is Tier 2
// (never accessible) or unknown, and reports ErrNotNeeded for default roots.
func (s *Service) Grant(ctx context.Context, root string) (domain.Consent, error) {
	if !s.interactive {
		return domain.Consent{}, fmt.Errorf("%w: consent can only be granted interactively on a TTY (SEC-ACC-07)", domain.ErrBlocked)
	}
	resolved, err := s.resolve(root)
	if err != nil {
		return domain.Consent{}, err
	}
	verdict, err := s.policy.RootVerdict(ctx, resolved)
	if err != nil {
		return domain.Consent{}, err
	}
	switch {
	case verdict.Blocked():
		return domain.Consent{}, fmt.Errorf("%w: %s: %s", domain.ErrBlocked, resolved, verdict.Reason)
	case verdict.Tier == domain.TierDefault:
		return domain.Consent{}, ErrNotNeeded
	}

	c := domain.Consent{Root: resolved, Tier: verdict.Tier, GrantedAt: s.now()}
	if err := s.repo.GrantConsent(ctx, c); err != nil {
		return domain.Consent{}, err
	}
	return c, nil
}

// Revoke revokes the consent for a root.
func (s *Service) Revoke(ctx context.Context, root string) error {
	resolved, err := s.resolve(root)
	if err != nil {
		return err
	}
	return s.repo.RevokeConsent(ctx, resolved, s.now())
}

// resolve makes a root absolute and clean (SEC-COD-01).
func (s *Service) resolve(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("%w: empty root", domain.ErrUsage)
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(s.policy.Home(), root)
	}
	return policy.NormalizePath(root)
}
