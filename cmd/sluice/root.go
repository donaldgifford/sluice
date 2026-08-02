// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/sluice/internal/config"
)

// buildInfo carries the ldflags-injected version metadata from main
// into the command tree.
type buildInfo struct {
	version string
	commit  string
	date    string
}

// newRootCmd assembles the sluice command tree. Errors are printed by
// main, not cobra, so the one-line-per-failure rendering of config
// errors reaches the user unwrapped.
func newRootCmd(info buildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:   "sluice",
		Short: "Manage an S3-backed Terraform provider network mirror from a declarative manifest",
		Long: "sluice reconciles a declarative HCL manifest of approved Terraform\n" +
			"provider versions against an S3-backed network mirror, with\n" +
			"plan/apply semantics and cryptographic verification at ingest.",
		Version:       info.version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate(fmt.Sprintf(
		"sluice %s (%s, %s)\n", info.version, info.commit, info.date))

	root.AddCommand(
		newValidateCmd(),
		newPlanCmd(),
		newApplyCmd(),
		newExportCmd(),
		newBootstrapCmd(),
	)
	return root
}

// configFlags is the manifest-source flag pair shared by every
// command that reads the manifest (all but bootstrap).
type configFlags struct {
	dir  string
	file string
}

// addConfigFlags registers --config-dir/--config-file on cmd and
// marks them mutually exclusive.
func addConfigFlags(cmd *cobra.Command) *configFlags {
	var cf configFlags
	cmd.Flags().StringVar(&cf.dir, "config-dir", "",
		"directory of *.hcl manifest files, merged as HCL bodies")
	cmd.Flags().StringVar(&cf.file, "config-file", "",
		"single manifest file")
	cmd.MarkFlagsMutuallyExclusive("config-dir", "config-file")
	return &cf
}

// load resolves the flag pair to a validated manifest.
func (cf *configFlags) load() (*config.Manifest, error) {
	switch {
	case cf.dir != "":
		return config.LoadDir(cf.dir)
	case cf.file != "":
		return config.LoadFile(cf.file)
	default:
		return nil, errors.New("one of --config-dir or --config-file is required")
	}
}
