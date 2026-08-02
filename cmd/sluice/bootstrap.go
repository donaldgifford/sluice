// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"github.com/spf13/cobra"
)

// newBootstrapCmd wires bootstrap: flag surface now, implementation in Phase 2.
func newBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap PATH...",
		Short: "Seed a manifest from the lock files under the given paths",
		Args:  cobra.MinimumNArgs(1),
	}
	cmd.Flags().String("out", "", "write the manifest to a file instead of stdout")

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		return errNotImplemented("bootstrap", "Phase 2")
	}
	return cmd
}
