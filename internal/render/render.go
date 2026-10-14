// Package render holds presentation helpers shared by the CLI and the TUI.
//
// It only formats values: it never decides access, reads files or prints. The
// sanitizer here is the single implementation used for untrusted file and app
// names (SEC-COD-02).
package render

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ansiPattern matches ANSI escape sequences (CSI, OSC and single-character
// escapes) that could come from hostile file or app names.
var ansiPattern = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b[@-Z\\-_]`)

// Sanitize strips ANSI escape sequences and control characters from untrusted
// text so terminal output cannot be manipulated (SEC-COD-02). It always
// returns valid UTF-8.
func Sanitize(s string) string {
	s = ansiPattern.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.IsControl(r):
			// drop other control characters
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if !utf8.ValidString(out) {
		out = strings.ToValidUTF8(out, "\uFFFD")
	}
	return out
}

// FormatBytes renders a byte count in human units.
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := "KMGTPE"
	if exp >= len(units) {
		exp = len(units) - 1
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), units[exp])
}

// Redact replaces the home prefix with "~" and, when mask is true, obscures
// file names in a path (SEC-TRN-06).
func Redact(path, home string, mask bool) string {
	if path == "" {
		return ""
	}
	if home != "" {
		path = strings.Replace(path, home, "~", 1)
	}
	if !mask {
		return path
	}
	return maskPath(path)
}

func maskPath(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 || idx == len(path)-1 {
		return path
	}
	dir := path[:idx+1]
	base := path[idx+1:]
	if dot := strings.LastIndex(base, "."); dot > 0 {
		return dir + "***" + base[dot:]
	}
	return dir + "***"
}
