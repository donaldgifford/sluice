// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/sluice/internal/publish"
	"github.com/donaldgifford/sluice/internal/registry"
)

// newApplyCmd wires apply: read → diff → confirm → fetch/verify/sign
// → ordered conditional publish.
func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile the mirror bucket to match the manifest",
		Args:  cobra.NoArgs,
	}
	cf := addConfigFlags(cmd)
	autoApprove := cmd.Flags().Bool("auto-approve", false, "skip the interactive confirmation")
	jsonOut := cmd.Flags().Bool("json", false, "emit the machine-readable plan (requires --auto-approve)")
	cosignKey := cmd.Flags().String("cosign-key", "",
		"cosign key reference (KMS URI or file); empty uses keyless OIDC")
	commit := cmd.Flags().String("authorizing-commit", os.Getenv("GITHUB_SHA"),
		"commit recorded in attestations (defaults to $GITHUB_SHA)")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		m, err := cf.load()
		if err != nil {
			return err
		}
		b, err := newBucket(cmd.Context(), m)
		if err != nil {
			return err
		}
		deps := applyDeps{
			bucket: b,
			fetch:  registry.New(),
			signer: publish.NewSigner(*cosignKey, *commit),
			log:    slog.Default(),
		}
		return runApply(cmd.Context(), m, deps,
			applyOpts{autoApprove: *autoApprove, jsonOut: *jsonOut},
			cmd.InOrStdin(), cmd.OutOrStdout())
	}
	return cmd
}
