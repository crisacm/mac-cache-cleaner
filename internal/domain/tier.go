package domain

import "fmt"

// Tier is a data-access tier. The classification table itself is compiled into
// internal/policy; the type lives here so results can carry their tier.
type Tier int

const (
	// TierDefault is scanned without asking (SEC-ACC-04).
	TierDefault Tier = 0
	// TierUserData requires recorded per-root consent and is report-only.
	TierUserData Tier = 1
	// TierSensitive is never traversed; no consent can enable it.
	TierSensitive Tier = 2
	// TierVolume requires an explicit --root plus consent; Time Machine is
	// never scanned.
	TierVolume Tier = 3
)

// Valid reports whether t is a known tier.
func (t Tier) Valid() bool { return t >= TierDefault && t <= TierVolume }

// String returns the numeric tier as text.
func (t Tier) String() string { return fmt.Sprintf("%d", int(t)) }

// RequiresConsent reports whether accessing the tier needs recorded consent.
func (t Tier) RequiresConsent() bool { return t == TierUserData || t == TierVolume }
