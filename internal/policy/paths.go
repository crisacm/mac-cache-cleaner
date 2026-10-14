package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// sep is the platform path separator.
const sep = string(os.PathSeparator)

// NormalizePath cleans and validates a path without touching the filesystem:
// it must be absolute, non-empty, free of NUL bytes and of ".." components
// (SEC-COD-01). Symlink resolution is a separate, filesystem-touching step.
func NormalizePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("%w: empty path", domain.ErrUsage)
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: NUL byte in path", domain.ErrUsage)
	}
	for _, part := range strings.Split(p, sep) {
		if part == ".." {
			return "", fmt.Errorf("%w: path traversal not allowed: %q", domain.ErrUsage, p)
		}
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%w: path must be absolute: %q", domain.ErrUsage, p)
	}
	return filepath.Clean(p), nil
}

// ResolvePath normalizes a path and resolves symlinks (SEC-COD-01). It touches
// the filesystem but reads no file contents. When the path does not exist it
// returns the normalized path.
func ResolvePath(p string) (string, error) {
	clean, err := NormalizePath(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return clean, nil
		}
		return "", fmt.Errorf("policy: resolve %q: %w", clean, err)
	}
	return resolved, nil
}

// UnderRoot reports whether path is root itself or inside root, using a
// path-boundary comparison: /a/Caches must not match /a/Caches-evil.
func UnderRoot(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	if path == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	return strings.HasPrefix(path, prefix)
}

// sipPrefixes are locations protected by System Integrity Protection.
var sipPrefixes = []string{
	"/System",
	"/bin",
	"/sbin",
	"/usr",
	"/etc",
	"/var",
	"/tmp",
	"/private/etc",
	"/private/var",
	"/private/tmp",
}

// sipExempt are protected prefixes that are actually writable and not SIP
// managed (e.g. Homebrew under /usr/local).
var sipExempt = []string{
	"/usr/local",
}

// IsSIP reports whether path is protected by System Integrity Protection
// (SEC-DEL-05). MCC never deletes SIP-protected paths.
func IsSIP(path string) bool {
	for _, ex := range sipExempt {
		if UnderRoot(ex, path) {
			return false
		}
	}
	for _, p := range sipPrefixes {
		if UnderRoot(p, path) {
			return true
		}
	}
	return false
}

// IsAppleBundleID reports whether a bundle identifier belongs to Apple
// (com.apple.*), which MCC never deletes (SEC-DEL-05, SEC-ORP-03).
func IsAppleBundleID(id string) bool {
	return id == "com.apple" || strings.HasPrefix(id, "com.apple.")
}

// joinHome joins a home directory and a home-relative pattern.
func joinHome(home, rel string) string {
	if strings.HasPrefix(rel, "/") {
		return filepath.Clean(rel)
	}
	return filepath.Join(home, rel)
}
