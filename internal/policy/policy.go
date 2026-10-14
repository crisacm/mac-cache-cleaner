// Package policy is the only place that decides what MCC may read, scan or
// delete (AGENTS.md §3, SECURITY.md §3).
//
// The classification table is compiled into the binary and configuration
// cannot downgrade a tier (SEC-ACC-03). Every access decision in the rest of
// the program goes through a *Policy.
package policy

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// Decision is the outcome of an access check.
type Decision string

const (
	// DecisionAllow permits the access.
	DecisionAllow Decision = "allow"
	// DecisionConsent permits nothing until recorded consent exists.
	DecisionConsent Decision = "consent_required"
	// DecisionDeny forbids the access outright.
	DecisionDeny Decision = "deny"
)

// Verdict is the result of an access check.
type Verdict struct {
	Path       string      `json:"path"`
	Tier       domain.Tier `json:"tier"`
	Decision   Decision    `json:"decision"`
	Reason     string      `json:"reason,omitempty"`
	ReportOnly bool        `json:"report_only,omitempty"`
}

// Allowed reports whether the access is permitted.
func (v Verdict) Allowed() bool { return v.Decision == DecisionAllow }

// Blocked reports whether the access is forbidden outright.
func (v Verdict) Blocked() bool { return v.Decision == DecisionDeny }

// NeedsConsent reports whether recorded consent is required first.
func (v Verdict) NeedsConsent() bool { return v.Decision == DecisionConsent }

// ConsentStore reads recorded, per-root consents (SEC-ACC-07). It is defined
// here because policy is the consumer.
type ConsentStore interface {
	ActiveConsent(ctx context.Context, root string, now time.Time) (domain.Consent, bool, error)
}

// Policy answers access questions for a home directory using recorded consents.
type Policy struct {
	home     string
	rules    []Rule
	consents ConsentStore
	now      func() time.Time
}

// New builds a policy for the given home directory. consents may be nil, in
// which case no consent is ever considered granted.
func New(home string, consents ConsentStore) *Policy {
	return &Policy{
		home:     home,
		rules:    buildRules(home),
		consents: consents,
		now:      time.Now,
	}
}

// Home returns the home directory the policy was built for.
func (p *Policy) Home() string { return p.home }

// Rules returns the compiled-in classification table (for `mcc permissions`).
func (p *Policy) Rules() []Rule {
	out := make([]Rule, len(p.rules))
	copy(out, p.rules)
	return out
}

// Classify returns the tier of a path and the rule that matched. ok is false
// when no compiled rule matches the path (fail closed).
func (p *Policy) Classify(path string) (tier domain.Tier, rule string, ok bool) {
	best := -1
	priority := -1
	for _, r := range p.rules {
		if path != r.Prefix && !UnderRoot(r.Prefix, path) {
			continue
		}
		pl := len(r.Prefix)
		pr := tierPriority(r.Tier)
		if pl > best || (pl == best && pr > priority) {
			best, priority, tier, rule, ok = pl, pr, r.Tier, r.Prefix, true
		}
	}
	return tier, rule, ok
}

// AllowlistRoots returns the Tier 0 roots that may be scanned and (with the
// proper checks) cleaned (SEC-DEL-05).
func (p *Policy) AllowlistRoots() []string {
	var out []string
	for _, r := range p.rules {
		if r.Tier == domain.TierDefault {
			out = append(out, r.Prefix)
		}
	}
	return out
}

// RootVerdict decides whether a scan root may be used (SEC-ACC-04).
func (p *Policy) RootVerdict(ctx context.Context, root string) (Verdict, error) {
	clean, err := NormalizePath(root)
	if err != nil {
		return Verdict{}, err
	}
	tier, rule, ok := p.Classify(clean)
	v := Verdict{Path: clean, Tier: tier}
	switch {
	case !ok:
		v.Decision = DecisionDeny
		v.Reason = "not a declared root"
		return v, nil
	case tier == domain.TierSensitive:
		v.Decision = DecisionDeny
		v.Reason = fmt.Sprintf("tier 2 (%s) is never accessed", rule)
		return v, nil
	case tier == domain.TierDefault:
		v.Decision = DecisionAllow
		return v, nil
	}

	// Tier 1 and Tier 3 need recorded, per-root consent (SEC-ACC-07).
	v.ReportOnly = tier == domain.TierUserData
	granted, err := p.hasConsent(ctx, clean)
	if err != nil {
		return Verdict{}, err
	}
	if granted {
		v.Decision = DecisionAllow
		v.Reason = "consent recorded"
		return v, nil
	}
	v.Decision = DecisionConsent
	v.Reason = "consent required for this root"
	return v, nil
}

// PathVerdict decides whether a path may be read during a walk of root
// (SEC-ACC-04, SEC-ACC-06). Path must already be absolute.
func (p *Policy) PathVerdict(root, path string) Verdict {
	v := Verdict{Path: path}
	rootTier, _, rootOK := p.Classify(root)

	if !UnderRoot(root, path) {
		v.Decision = DecisionDeny
		v.Tier = rootTier
		v.Reason = "outside declared root"
		return v
	}
	tier, rule, ok := p.Classify(path)
	if ok {
		v.Tier = tier
	} else {
		v.Tier = rootTier
	}
	if ok && tier == domain.TierSensitive {
		v.Decision = DecisionDeny
		v.Reason = fmt.Sprintf("tier 2 (%s) is never accessed", rule)
		return v
	}
	if p.isSIP(path) {
		v.Decision = DecisionDeny
		v.Reason = "SIP-protected path"
		return v
	}
	if rootOK && rootTier == domain.TierUserData {
		v.ReportOnly = true
	}
	v.Decision = DecisionAllow
	return v
}

// DeleteVerdict decides whether a path may be deleted by the cleaner
// (SEC-DEL-05). It refuses allowlisted roots themselves, anything outside the
// allowlist, Tier 1/2/3 locations, SIP paths and com.apple.* bundle IDs.
func (p *Policy) DeleteVerdict(path, bundleID string) Verdict {
	v := Verdict{Path: path, Decision: DecisionDeny}
	if bundleID != "" && IsAppleBundleID(bundleID) {
		v.Reason = "com.apple.* is never deleted"
		return v
	}
	tier, rule, ok := p.Classify(path)
	if !ok {
		v.Reason = "outside the allowlist"
		return v
	}
	v.Tier = tier
	if tier != domain.TierDefault {
		v.Reason = fmt.Sprintf("tier %s is not cleanable", tier)
		return v
	}
	for _, allowed := range p.AllowlistRoots() {
		if path == allowed {
			v.Reason = fmt.Sprintf("refuse to delete allowlisted root %s", rule)
			return v
		}
	}
	if p.isSIP(path) {
		v.Reason = "SIP-protected path"
		return v
	}
	v.Decision = DecisionAllow
	v.Reason = ""
	return v
}

// isSIP reports whether path is SIP-protected. A path inside the user's home
// is never SIP-protected, which also keeps test trees under /var (a symlink to
// /private/var) usable as stand-in homes.
func (p *Policy) isSIP(path string) bool {
	if UnderRoot(p.home, path) {
		return false
	}
	return IsSIP(path)
}

// hasConsent reports whether recorded consent is in force for root.
func (p *Policy) hasConsent(ctx context.Context, root string) (bool, error) {
	if p.consents == nil {
		return false, nil
	}
	_, ok, err := p.consents.ActiveConsent(ctx, root, p.now())
	if err != nil {
		return false, fmt.Errorf("policy: read consent for %q: %w", root, err)
	}
	return ok, nil
}

// EnsureNotRoot refuses to run with euid 0 (SEC-ACC-01).
func EnsureNotRoot() error { return CheckNotRoot(os.Geteuid()) }

// CheckNotRoot is EnsureNotRoot with an injected euid, for testing.
func CheckNotRoot(euid int) error {
	if euid == 0 {
		return fmt.Errorf("%w: refusing to run as root (SEC-ACC-01)", domain.ErrBlocked)
	}
	return nil
}
