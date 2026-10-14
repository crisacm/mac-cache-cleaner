// Command mcc is the Mac Cache Cleaner CLI.
//
// This file contains wiring only (manual dependency injection). Business logic
// lives in internal/ services; rendering lives in internal/cli. See AGENTS.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	// The full command tree is wired in the cli step; until then, exit cleanly.
	fmt.Fprintln(os.Stderr, "mcc: not yet wired")
	os.Exit(1)
}
