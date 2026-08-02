// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"fmt"
	"os"

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
	out := cmd.Flags().String("out", "", "write to a file instead of stdout")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		m, err := cf.load()
		if err != nil {
			return err
		}
		data, err := config.ExportJSON(m)
		if err != nil {
			return err
		}
		if *out != "" {
			if err := os.WriteFile(*out, data, 0o644); err != nil {
				return fmt.Errorf("writing %s: %w", *out, err)
			}
			return nil
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	}
	return cmd
}
