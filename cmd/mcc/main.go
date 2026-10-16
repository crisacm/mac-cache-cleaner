// Command mcc is the Mac Cache Cleaner CLI.
//
// This file contains wiring only (manual dependency injection). Business logic
// lives in internal/ services; rendering lives in internal/cli. See AGENTS.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/crisacm/mac-cache-cleaner/internal/cli"
	"github.com/crisacm/mac-cache-cleaner/internal/config"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/store"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// signal.NotifyContext swallows SIGINT (Ctrl+C only cancels ctx). Arm a
	// second Ctrl+C as a kill switch so a command that ignores cancellation can
	// still be terminated. The TUI is skipped: bubbletea exits on the first
	// signal and restores the terminal, and a forced os.Exit would corrupt it.
	if !isTUICommand(os.Args[1:]) {
		go watchInterrupts()
	}

	// MCC refuses to run as root (SEC-ACC-01).
	if err := policy.EnsureNotRoot(); err != nil {
		fmt.Fprintln(os.Stderr, "mcc:", err)
		return domain.ExitBlocked
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcc: cannot determine home directory:", err)
		return domain.ExitError
	}

	cfg, err := config.Load(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcc:", err)
		return exitCode(err)
	}

	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcc:", err)
		return domain.ExitError
	}
	defer func() { _ = st.Close() }()

	pol := policy.New(home, st)

	isTTY := false
	if fi, err := os.Stdin.Stat(); err == nil {
		isTTY = fi.Mode()&os.ModeCharDevice != 0
	}
	if fi, err := os.Stdout.Stat(); err == nil {
		isTTY = isTTY && fi.Mode()&os.ModeCharDevice != 0
	}

	app := cli.New(home, cfg, pol, st, os.Stdin, os.Stdout, os.Stderr, isTTY)
	return app.Execute(ctx, os.Args[1:])
}

// watchInterrupts prints a hint on the first SIGINT/SIGTERM and forces the exit
// on the second, so Ctrl+C always terminates the process even if a running
// command is not observing its context.
func watchInterrupts() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	first := true
	for range sigs {
		if first {
			first = false
			fmt.Fprintln(os.Stderr, "mcc: cancelando… (pulsa Ctrl+C otra vez para forzar la salida)")
			continue
		}
		os.Exit(domain.ExitCanceled)
	}
}

// isTUICommand reports whether the first non-flag argument is the tui command,
// which manages its own signal handling and terminal state.
func isTUICommand(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a == "tui"
	}
	return false
}

func exitCode(err error) int {
	if err == nil {
		return domain.ExitOK
	}
	return domain.ExitError
}
