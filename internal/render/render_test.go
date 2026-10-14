package render

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeStripsEscapesAndControls(t *testing.T) {
	require.Equal(t, "evilname", Sanitize("evil\x1b[31mname"))
	require.Equal(t, "a\tb", Sanitize("a\tb"))
	require.Equal(t, "ab", Sanitize("a\x00b"))
	require.NotContains(t, Sanitize("\x1b]0;pwned\x07x"), "\x1b")
	require.Equal(t, "ok", Sanitize("ok"))
}

func TestFormatBytes(t *testing.T) {
	require.Equal(t, "0 B", FormatBytes(0))
	require.Equal(t, "512 B", FormatBytes(512))
	require.Equal(t, "1.0 KiB", FormatBytes(1024))
	require.Equal(t, "1.0 GiB", FormatBytes(1<<30))
}

func TestRedact(t *testing.T) {
	require.Equal(t, "~/Library/Caches", Redact("/Users/x/Library/Caches", "/Users/x", false))
	require.Equal(t, "~/Library/Caches/***", Redact("/Users/x/Library/Caches/secret", "/Users/x", true))
	require.Equal(t, "~/Documents/***.pdf", Redact("/Users/x/Documents/report.pdf", "/Users/x", true))
	require.Equal(t, "", Redact("", "/Users/x", true))
}
