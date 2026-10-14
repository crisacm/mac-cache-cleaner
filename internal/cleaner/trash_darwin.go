//go:build darwin

package cleaner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// osascriptPath is the absolute path to osascript (SEC-DEL-09).
const osascriptPath = "/usr/bin/osascript"

// trashScript asks Finder to move the file at argv 1 to the Trash. The path is
// passed as an argument, never interpolated into the script text (SEC-DEL-09).
const trashScript = `on run argv
tell application "Finder" to delete (POSIX file (item 1 of argv) as alias)
end run`

// fallbackTrash uses Finder via osascript when a direct rename is not possible
// (for example across volumes).
func fallbackTrash(ctx context.Context, path string) error {
	// #nosec G204 -- absolute binary, explicit argv, no shell; path is passed
	// as an argument, never interpolated into script text (SEC-DEL-09).
	cmd := exec.CommandContext(ctx, osascriptPath, "-e", trashScript, path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
