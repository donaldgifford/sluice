// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

// Package main is the entry point for the sluice CLI.
//
// sluice reconciles a declarative HCL manifest of approved Terraform
// providers against an S3-backed network mirror. The command surface
// is validate, plan, apply, export, and bootstrap; every manifest-
// reading command shares the --config-dir/--config-file pair and the
// one-line-per-failure error rendering from internal/config.
package main
