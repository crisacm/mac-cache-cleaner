package consent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
)

type fakeRepo struct{ consents map[string]domain.Consent }

func newFakeRepo() *fakeRepo { return &fakeRepo{consents: map[string]domain.Consent{}} }

func (f *fakeRepo) Consents(context.Context) ([]domain.Consent, error) {
	out := make([]domain.Consent, 0, len(f.consents))
	for _, c := range f.consents {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeRepo) GrantConsent(_ context.Context, c domain.Consent) error {
	f.consents[c.Root] = c
	return nil
}

func (f *fakeRepo) RevokeConsent(_ context.Context, root string, at time.Time) error {
	c, ok := f.consents[root]
	if !ok {
		return domain.ErrNotFound
	}
	c.RevokedAt = at
	f.consents[root] = c
	return nil
}

const home = "/Users/tester"

func TestGrantRefusedWhenNotInteractive(t *testing.T) {
	svc := New(newFakeRepo(), policy.New(home, nil), false)
	_, err := svc.Grant(context.Background(), filepath.Join(home, "Documents"))
	require.ErrorIs(t, err, domain.ErrBlocked, "consent must not be grantable non-interactively (SEC-ACC-07)")
}

func TestGrantTier1Interactive(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo, policy.New(home, nil), true)
	c, err := svc.Grant(context.Background(), filepath.Join(home, "Documents"))
	require.NoError(t, err)
	require.Equal(t, domain.TierUserData, c.Tier)
	require.Equal(t, filepath.Join(home, "Documents"), c.Root)
	require.Contains(t, repo.consents, c.Root)
}

func TestGrantTier2Refused(t *testing.T) {
	svc := New(newFakeRepo(), policy.New(home, nil), true)
	_, err := svc.Grant(context.Background(), filepath.Join(home, "Library", "Keychains"))
	require.ErrorIs(t, err, domain.ErrBlocked, "tier 2 can never be granted")
}

func TestGrantDefaultRootNotNeeded(t *testing.T) {
	svc := New(newFakeRepo(), policy.New(home, nil), true)
	_, err := svc.Grant(context.Background(), filepath.Join(home, "Library", "Caches"))
	require.ErrorIs(t, err, ErrNotNeeded)
}

func TestGrantRelativeRootResolved(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo, policy.New(home, nil), true)
	c, err := svc.Grant(context.Background(), "Downloads")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "Downloads"), c.Root)
}

func TestRevoke(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo, policy.New(home, nil), true)
	doc := filepath.Join(home, "Documents")
	_, err := svc.Grant(context.Background(), doc)
	require.NoError(t, err)

	require.NoError(t, svc.Revoke(context.Background(), doc))
	require.False(t, repo.consents[doc].Active(time.Now()), "revoked consent must be inactive")

	require.ErrorIs(t, svc.Revoke(context.Background(), filepath.Join(home, "Music")), domain.ErrNotFound)
}

func TestVerdict(t *testing.T) {
	svc := New(newFakeRepo(), policy.New(home, nil), true)
	v, err := svc.Verdict(context.Background(), filepath.Join(home, "Pictures"))
	require.NoError(t, err)
	require.Equal(t, domain.TierUserData, v.Tier)
}
