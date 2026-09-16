// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

// Package main is the entry point for the sluice CLI.
//
// sluice reconciles a declarative HCL manifest of approved Terraform
// providers against an S3-backed network mirror. The command surface
// is validate, plan, apply, export, and bootstrap; every manifest-
// reading command shares the --config-dir/--config-file pair, the
// --s3-endpoint/--s3-path-style backend overrides (explicit flags beat
// SLUICE_S3_ENDPOINT/SLUICE_S3_PATH_STYLE, which beat the manifest),
// and the one-line-per-failure error rendering from internal/config.
package main
