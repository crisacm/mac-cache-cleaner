package policy

import (
	"sort"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// Rule is one entry of the compiled-in tier table (SEC-ACC-03).
type Rule struct {
	Prefix string      `json:"prefix"`
	Tier   domain.Tier `json:"tier"`
	Note   string      `json:"note,omitempty"`
}

// relativeRules are home-relative patterns; absoluteRules are absolute.
//
// This table is compiled into the binary: configuration MUST NOT be able to
// downgrade a tier (SEC-ACC-03).
var relativeRules = []Rule{
	// Tier 2 — sensitive, never accessed; no consent can enable it.
	{Prefix: "Library/Keychains", Tier: domain.TierSensitive, Note: "keychain"},
	{Prefix: "Library/Mobile Documents", Tier: domain.TierSensitive, Note: "iCloud"},
	{Prefix: "Library/Messages", Tier: domain.TierSensitive, Note: "Messages"},
	{Prefix: "Library/Mail", Tier: domain.TierSensitive, Note: "Mail"},
	{Prefix: "Library/Safari", Tier: domain.TierSensitive, Note: "Safari data"},
	{Prefix: "Library/Cookies", Tier: domain.TierSensitive, Note: "browser cookies"},
	{Prefix: "Library/Application Support/Google/Chrome", Tier: domain.TierSensitive, Note: "browser profile/credentials"},
	{Prefix: "Library/Application Support/Firefox", Tier: domain.TierSensitive, Note: "browser profile/credentials"},
	{Prefix: "Library/Application Support/BraveSoftware", Tier: domain.TierSensitive, Note: "browser profile/credentials"},
	{Prefix: "Library/Application Support/Microsoft Edge", Tier: domain.TierSensitive, Note: "browser profile/credentials"},
	{Prefix: "Library/Application Support/1Password", Tier: domain.TierSensitive, Note: "password manager"},
	{Prefix: "Library/Application Support/Bitwarden", Tier: domain.TierSensitive, Note: "password manager"},
	{Prefix: ".ssh", Tier: domain.TierSensitive, Note: "SSH keys"},
	{Prefix: ".gnupg", Tier: domain.TierSensitive, Note: "GnuPG"},
	{Prefix: ".aws", Tier: domain.TierSensitive, Note: "AWS credentials"},
	{Prefix: ".kube", Tier: domain.TierSensitive, Note: "Kubernetes config"},

	// Tier 1 — user data; opt-in per root, report-only.
	{Prefix: "Documents", Tier: domain.TierUserData, Note: "user documents"},
	{Prefix: "Desktop", Tier: domain.TierUserData, Note: "user desktop"},
	{Prefix: "Downloads", Tier: domain.TierUserData, Note: "user downloads"},
	{Prefix: "Pictures", Tier: domain.TierUserData, Note: "user pictures"},
	{Prefix: "Movies", Tier: domain.TierUserData, Note: "user movies"},
	{Prefix: "Music", Tier: domain.TierUserData, Note: "user music"},
	{Prefix: "Public", Tier: domain.TierUserData, Note: "user public"},

	// Tier 0 — default scan; metadata only.
	{Prefix: "Applications", Tier: domain.TierDefault, Note: "user applications"},
	{Prefix: "Library/Caches", Tier: domain.TierDefault, Note: "caches"},
	{Prefix: "Library/Logs", Tier: domain.TierDefault, Note: "logs"},
	{Prefix: "Library/Application Support", Tier: domain.TierDefault, Note: "app data"},
	{Prefix: "Library/Containers", Tier: domain.TierDefault, Note: "sandbox containers"},
	{Prefix: "Library/Group Containers", Tier: domain.TierDefault, Note: "group containers"},
	{Prefix: "Library/HTTPStorages", Tier: domain.TierDefault, Note: "HTTP storages"},
	{Prefix: "Library/WebKit", Tier: domain.TierDefault, Note: "WebKit data"},
	{Prefix: "Library/Saved Application State", Tier: domain.TierDefault, Note: "saved app state"},
	{Prefix: "Library/Developer/Xcode/DerivedData", Tier: domain.TierDefault, Note: "Xcode DerivedData"},
	{Prefix: ".gradle/caches", Tier: domain.TierDefault, Note: "Gradle caches"},
	{Prefix: ".android/avd", Tier: domain.TierDefault, Note: "Android AVD"},
	{Prefix: ".npm", Tier: domain.TierDefault, Note: "npm cache"},
	{Prefix: ".cache/pnpm", Tier: domain.TierDefault, Note: "pnpm store"},
	{Prefix: "Library/pnpm/store", Tier: domain.TierDefault, Note: "pnpm store"},
	{Prefix: "go/pkg/mod", Tier: domain.TierDefault, Note: "Go module cache"},
}

var absoluteRules = []Rule{
	{Prefix: "/Applications", Tier: domain.TierDefault, Note: "system applications"},
	{Prefix: "/Volumes", Tier: domain.TierVolume, Note: "external volumes, network, Time Machine"},
	{Prefix: "/Network", Tier: domain.TierVolume, Note: "network mounts"},
}

// tierPriority orders tiers by restrictiveness for longest-prefix tie-breaks:
// Tier 2 (never) is stronger than Tier 3 (consent), then Tier 1, then Tier 0.
func tierPriority(t domain.Tier) int {
	switch t {
	case domain.TierSensitive:
		return 3
	case domain.TierVolume:
		return 2
	case domain.TierUserData:
		return 1
	default:
		return 0
	}
}

// buildRules expands the compiled tables against a home directory.
func buildRules(home string) []Rule {
	rules := make([]Rule, 0, len(relativeRules)+len(absoluteRules))
	for _, r := range relativeRules {
		r.Prefix = joinHome(home, r.Prefix)
		rules = append(rules, r)
	}
	rules = append(rules, absoluteRules...)
	// Longest prefixes first so the most specific rule wins.
	sort.SliceStable(rules, func(i, j int) bool {
		return len(rules[i].Prefix) > len(rules[j].Prefix)
	})
	return rules
}
