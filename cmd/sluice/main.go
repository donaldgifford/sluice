// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/donaldgifford/sluice/internal/publish"
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
		// Plan's changes-present signal is not a failure: the plan is
		// already on stdout, so exit 2 silently.
		if !errors.Is(err, errChangesPresent) {
			// Config errors already render one actionable line per
			// failure; print them raw rather than through cobra.
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(exitCode(err))
	}
}

// exitCode implements the spec's contract: 0 success/no changes,
// 1 error, 2 plan changes with --detailed-exitcode, 3 conditional
// write conflict.
func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errChangesPresent):
		return 2
	case errors.Is(err, publish.ErrConflict):
		return 3
	default:
		return 1
	}
}
