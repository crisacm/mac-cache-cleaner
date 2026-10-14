package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

type fakeConsents struct {
	active map[string]domain.Consent
}

func (f fakeConsents) ActiveConsent(_ context.Context, root string, now time.Time) (domain.Consent, bool, error) {
	c, ok := f.active[root]
	if !ok || !c.Active(now) {
		return domain.Consent{}, false, nil
	}
	return c, true, nil
}

const home = "/Users/test"

func newPolicy(consents ConsentStore) *Policy { return New(home, consents) }

func TestNormalizePathRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"absolute ok", "/Users/test/Library/Caches", true},
		{"cleans dots", "/Users/test/Library/./Caches", true},
		{"empty", "", false},
		{"relative", "Library/Caches", false},
		{"traversal", "/Users/test/../etc/passwd", false},
		{"nul", "/Users/test/a\x00b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePath(tt.in)
			if tt.ok {
				require.NoError(t, err)
				require.True(t, filepath.IsAbs(got))
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestUnderRootBoundary(t *testing.T) {
	require.True(t, UnderRoot("/a/Caches", "/a/Caches/com.example"))
	require.True(t, UnderRoot("/a/Caches", "/a/Caches"))
	require.False(t, UnderRoot("/a/Caches", "/a/Caches-evil"), "path boundary must not match a sibling prefix")
	require.False(t, UnderRoot("/a/Caches", "/a"))
}

func TestClassifyTiers(t *testing.T) {
	p := newPolicy(nil)
	tests := []struct {
		path string
		tier domain.Tier
	}{
		{"/Users/test/Library/Caches/com.foo", domain.TierDefault},
		{"/Users/test/Library/Logs", domain.TierDefault},
		{"/Applications/Safari.app", domain.TierDefault},
		{"/Users/test/Library/Developer/Xcode/DerivedData", domain.TierDefault},
		{"/Users/test/Documents", domain.TierUserData},
		{"/Users/test/Downloads/thing", domain.TierUserData},
		{"/Users/test/Library/Keychains", domain.TierSensitive},
		{"/Users/test/Library/Keychains/login.keychain-db", domain.TierSensitive},
		{"/Users/test/.ssh/id_ed25519", domain.TierSensitive},
		{"/Users/test/Library/Mobile Documents/com~apple~x", domain.TierSensitive},
		{"/Volumes/External/backup", domain.TierVolume},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			tier, _, ok := p.Classify(tt.path)
			require.True(t, ok)
			require.Equal(t, tt.tier, tier)
		})
	}
}

func TestTier2WinsOverTier0(t *testing.T) {
	p := newPolicy(nil)
	// Application Support is Tier 0, but the Chrome profile under it is Tier 2.
	tier, _, ok := p.Classify("/Users/test/Library/Application Support/Google/Chrome/Default/Login Data")
	require.True(t, ok)
	require.Equal(t, domain.TierSensitive, tier)
}

func TestRootVerdict(t *testing.T) {
	now := time.Now()
	consents := fakeConsents{active: map[string]domain.Consent{
		"/Users/test/Documents": {Root: "/Users/test/Documents", Tier: domain.TierUserData, GrantedAt: now},
	}}
	p := newPolicy(consents)
	ctx := context.Background()

	// Tier 0: allowed without consent.
	v, err := p.RootVerdict(ctx, "/Users/test/Library/Caches")
	require.NoError(t, err)
	require.True(t, v.Allowed())

	// Tier 1 without consent: consent required.
	v, err = p.RootVerdict(ctx, "/Users/test/Downloads")
	require.NoError(t, err)
	require.True(t, v.NeedsConsent())
	require.True(t, v.ReportOnly || v.NeedsConsent())

	// Tier 1 with recorded consent: allowed and report-only.
	v, err = p.RootVerdict(ctx, "/Users/test/Documents")
	require.NoError(t, err)
	require.True(t, v.Allowed())
	require.True(t, v.ReportOnly)

	// Tier 2: denied outright, even with a consent row present.
	consents.active["/Users/test/Library/Keychains"] = domain.Consent{
		Root: "/Users/test/Library/Keychains", Tier: domain.TierSensitive, GrantedAt: now,
	}
	v, err = p.RootVerdict(ctx, "/Users/test/Library/Keychains")
	require.NoError(t, err)
	require.True(t, v.Blocked(), "tier 2 must never be allowed")

	// Tier 3 without consent: consent required.
	v, err = p.RootVerdict(ctx, "/Volumes/External")
	require.NoError(t, err)
	require.True(t, v.NeedsConsent())

	// Unknown path: fail closed.
	v, err = p.RootVerdict(ctx, "/Users/test/Random")
	require.NoError(t, err)
	require.True(t, v.Blocked())
}

func TestRootVerdictDoesNotGrantConsentByFlag(t *testing.T) {
	// There is no code path that turns a flag into consent: with an empty
	// consent store a Tier 1 root always requires consent (SEC-ACC-07).
	p := newPolicy(fakeConsents{active: map[string]domain.Consent{}})
	v, err := p.RootVerdict(context.Background(), "/Users/test/Documents")
	require.NoError(t, err)
	require.True(t, v.NeedsConsent())
}

func TestPathVerdict(t *testing.T) {
	p := newPolicy(nil)
	root := "/Users/test/Library/Caches"

	require.True(t, p.PathVerdict(root, "/Users/test/Library/Caches/com.foo").Allowed())
	// Outside the declared root.
	require.True(t, p.PathVerdict(root, "/Users/test/Library/Logs/x").Blocked())
	// Tier 2 path is denied even if somehow under a Tier 0 root.
	require.True(t, p.PathVerdict(root, "/Users/test/Library/Keychains/x").Blocked())
	// Sibling-prefix escape.
	require.True(t, p.PathVerdict(root, "/Users/test/Library/Caches-evil/x").Blocked())
}

func TestDeleteVerdict(t *testing.T) {
	p := newPolicy(nil)

	// Allowed: a cache inside an allowlisted root.
	require.True(t, p.DeleteVerdict("/Users/test/Library/Caches/com.example", "com.example").Allowed())

	// Refuse the allowlisted root itself.
	require.True(t, p.DeleteVerdict("/Users/test/Library/Caches", "").Blocked())

	// Refuse outside the allowlist.
	require.True(t, p.DeleteVerdict("/Users/test/Documents/report.pdf", "").Blocked())

	// Refuse Tier 2 and Tier 3.
	require.True(t, p.DeleteVerdict("/Users/test/Library/Keychains/login.keychain-db", "").Blocked())
	require.True(t, p.DeleteVerdict("/Volumes/External/data", "").Blocked())

	// Refuse com.apple.* and SIP paths.
	require.True(t, p.DeleteVerdict("/Users/test/Library/Caches/com.apple.Safari", "com.apple.Safari").Blocked())
	require.True(t, p.DeleteVerdict("/usr/lib/thing", "").Blocked())
}

func TestIsAppleBundleID(t *testing.T) {
	require.True(t, IsAppleBundleID("com.apple.Safari"))
	require.True(t, IsAppleBundleID("com.apple"))
	require.False(t, IsAppleBundleID("com.example.app"))
	require.False(t, IsAppleBundleID("com.appleby.app"))
}

func TestIsSIP(t *testing.T) {
	require.True(t, IsSIP("/System/Library/CoreServices"))
	require.True(t, IsSIP("/usr/bin/python3"))
	require.False(t, IsSIP("/usr/local/bin/brew"))
	require.False(t, IsSIP("/Users/test/Library/Caches"))
}

func TestCheckNotRoot(t *testing.T) {
	require.ErrorIs(t, CheckNotRoot(0), domain.ErrBlocked)
	require.NoError(t, CheckNotRoot(501))
}

func TestResolvePathRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "root")
	link := filepath.Join(inside, "escape")
	require.NoError(t, os.MkdirAll(inside, 0o750))
	require.NoError(t, os.Symlink("/etc", link))

	resolved, err := ResolvePath(link)
	require.NoError(t, err)
	require.False(t, UnderRoot(inside, resolved), "symlink must resolve outside the root")

	p := newPolicy(nil)
	require.True(t, p.PathVerdict(inside, resolved).Blocked(), "resolved symlink must fall outside the root")

	// A normal path resolves to itself.
	same, err := ResolvePath(filepath.Join(inside, "child"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(inside, "child"), same)
}

func TestAllowlistRootsAreTier0(t *testing.T) {
	p := newPolicy(nil)
	roots := p.AllowlistRoots()
	require.Contains(t, roots, "/Users/test/Library/Caches")
	require.Contains(t, roots, "/Applications")
	require.NotContains(t, roots, "/Users/test/Documents")
}
