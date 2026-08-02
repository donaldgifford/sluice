// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"github.com/spf13/cobra"
)

func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show what apply would change on the mirror (read-only)",
		Args:  cobra.NoArgs,
	}
	cf := addConfigFlags(cmd)
	cmd.Flags().Bool("json", false, "emit the machine-readable plan schema")
	cmd.Flags().Bool("detailed-exitcode", false,
		"exit 2 when changes are present (0 clean, 1 error)")

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		if _, err := cf.load(); err != nil {
			return err
		}
		return errNotImplemented("plan", "Phase 4")
	}
	return cmd
}
