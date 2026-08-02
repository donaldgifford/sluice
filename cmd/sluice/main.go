// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"fmt"
	"log/slog"
	"os"
)

// Injected via -ldflags at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	root := newRootCmd(buildInfo{version: version, commit: commit, date: date})
	if err := root.Execute(); err != nil {
		// Config errors already render one actionable line per
		// failure; print them raw rather than through cobra.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// errNotImplemented marks a command whose implementation lands in a
// later IMPL-0001 phase; the flag surface is wired ahead of it.
func errNotImplemented(command, phase string) error {
	return fmt.Errorf("sluice %s is not implemented yet (IMPL-0001 %s)", command, phase)
}
