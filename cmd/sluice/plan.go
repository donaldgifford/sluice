// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"github.com/spf13/cobra"
)

// newPlanCmd wires plan: read actual state, diff against the
// manifest, print. Never writes.
func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show what apply would change on the mirror (read-only)",
		Args:  cobra.NoArgs,
	}
	cf := addConfigFlags(cmd)
	jsonOut := cmd.Flags().Bool("json", false, "emit the machine-readable plan schema")
	detailed := cmd.Flags().Bool("detailed-exitcode", false,
		"exit 2 when changes are present (0 clean, 1 error)")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		m, err := cf.load()
		if err != nil {
			return err
		}
		b, err := newBucket(cmd.Context(), m)
		if err != nil {
			return err
		}
		return runPlan(cmd.Context(), m, b,
			planOpts{jsonOut: *jsonOut, detailed: *detailed}, cmd.OutOrStdout())
	}
	return cmd
}
