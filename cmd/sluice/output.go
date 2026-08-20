// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// writeOutput writes data to path if set, otherwise to cmd's stdout —
// the shared --out contract for commands that emit a document.
func writeOutput(cmd *cobra.Command, data []byte, path string) error {
	if path == "" {
		_, err := cmd.OutOrStdout().Write(data)
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
