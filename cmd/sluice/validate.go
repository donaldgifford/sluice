// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Parse and schema-check the manifest (no network)",
		Args:  cobra.NoArgs,
	}
	cf := addConfigFlags(cmd)

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		m, err := cf.load()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(),
			"Valid: %d provider(s), %d default platform(s)\n",
			len(m.Providers), len(m.Mirror.Platforms))
		return err
	}
	return cmd
}
