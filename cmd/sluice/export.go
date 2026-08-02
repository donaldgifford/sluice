// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"github.com/spf13/cobra"
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Emit the canonical JSON projection of the approved set",
		Args:  cobra.NoArgs,
	}
	cf := addConfigFlags(cmd)
	cmd.Flags().String("out", "", "write to a file instead of stdout")

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		if _, err := cf.load(); err != nil {
			return err
		}
		return errNotImplemented("export", "Phase 2")
	}
	return cmd
}
