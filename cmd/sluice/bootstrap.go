// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"github.com/spf13/cobra"

	"github.com/donaldgifford/sluice/internal/bootstrap"
)

// newBootstrapCmd wires bootstrap: seed a manifest from the lock
// files under the given paths, to stdout or --out.
func newBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap PATH...",
		Short: "Seed a manifest from the lock files under the given paths",
		Args:  cobra.MinimumNArgs(1),
	}
	out := cmd.Flags().String("out", "", "write the manifest to a file instead of stdout")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		data, err := bootstrap.Generate(args)
		if err != nil {
			return err
		}
		return writeOutput(cmd, data, *out)
	}
	return cmd
}
