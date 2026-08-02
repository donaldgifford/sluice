// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"github.com/spf13/cobra"
)

// newApplyCmd wires apply: flag surface now, implementation in Phase 4.
func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile the mirror bucket to match the manifest",
		Args:  cobra.NoArgs,
	}
	cf := addConfigFlags(cmd)
	cmd.Flags().Bool("auto-approve", false, "skip the interactive confirmation")
	cmd.Flags().Bool("json", false, "emit machine-readable progress")

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		if _, err := cf.load(); err != nil {
			return err
		}
		return errNotImplemented("apply", "Phase 4")
	}
	return cmd
}
