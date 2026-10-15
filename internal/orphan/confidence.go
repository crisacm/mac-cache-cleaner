package orphan

import (
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
)

// teamIDLen is the length of an Apple team identifier prefix in Group
// Containers names (e.g. ABCDE12345.com.example.app).
const teamIDLen = 10

// normalizeCandidate turns a leftover directory/file name into a candidate
// bundle identifier. It strips Saved Application State / plist suffixes and a
// leading team identifier.
func normalizeCandidate(name string) string {
	name = strings.TrimSuffix(name, ".savedState")
	name = strings.TrimSuffix(name, ".plist")
	if isTeamID(name[:min(len(name), teamIDLen)]) && len(name) > teamIDLen && name[teamIDLen] == '.' {
		name = name[teamIDLen+1:]
	}
	return strings.TrimSpace(name)
}

// isTeamID reports whether s looks like an Apple team identifier.
func isTeamID(s string) bool {
	if len(s) != teamIDLen {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// Classify assigns a confidence level to a candidate identifier (SEC-ORP-01).
//
//   - high:   the name is a full reverse-DNS bundle ID (>= 3 components).
//   - medium: the name is a team prefix (2 components) or shares a team prefix
//     with an installed application.
//   - low:    anything else (a bare name).
func Classify(name string, installed map[string]struct{}) domain.Confidence {
	parts := strings.Split(name, ".")
	switch {
	case len(parts) >= 3:
		return domain.ConfidenceHigh
	case len(parts) == 2:
		return domain.ConfidenceMedium
	}
	// Bare name: medium only if some installed app shares this prefix.
	for id := range installed {
		if strings.HasPrefix(id, name+".") {
			return domain.ConfidenceMedium
		}
	}
	return domain.ConfidenceLow
}

// ConfidenceReason returns a static explanation of a confidence level. It never
// contains filesystem-derived text, so it is safe in tool descriptions and
// error messages (SEC-MCP-06).
func ConfidenceReason(c domain.Confidence) string {
	switch c {
	case domain.ConfidenceHigh:
		return "full reverse-DNS bundle identifier"
	case domain.ConfidenceMedium:
		return "team prefix or two-component identifier"
	default:
		return "name only, not a reverse-DNS identifier"
	}
}

// degrade lowers a confidence level when app discovery is incomplete, because
// an inaccessible application location may hide a matching app (SEC-ORP-02).
func degrade(c domain.Confidence) domain.Confidence {
	switch c {
	case domain.ConfidenceHigh:
		return domain.ConfidenceMedium
	case domain.ConfidenceMedium:
		return domain.ConfidenceLow
	default:
		return domain.ConfidenceLow
	}
}

// isAppleName reports whether a candidate belongs to Apple and must be
// excluded (SEC-ORP-03).
func isAppleName(name string) bool {
	return policy.IsAppleBundleID(name)
}
