// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"github.com/spf13/cobra"

	"github.com/donaldgifford/sluice/internal/config"
)

// newExportCmd wires export: the canonical JSON projection of the
// approved set, to stdout or --out. No network, no side effects.
func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Emit the canonical JSON projection of the approved set",
		Args:  cobra.NoArgs,
	}
	cf := addConfigFlags(cmd)
	bf := addBackendFlags(cmd)
	out := cmd.Flags().String("out", "", "write to a file instead of stdout")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		m, err := cf.load()
		if err != nil {
			return err
		}
		if err := bf.apply(m); err != nil {
			return err
		}
		data, err := config.ExportJSON(m)
		if err != nil {
			return err
		}
		return writeOutput(cmd, data, *out)
	}
	return cmd
}
