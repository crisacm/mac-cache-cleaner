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

func exitCode(err error) int {
	if err == nil {
		return domain.ExitOK
	}
	return domain.ExitError
}
