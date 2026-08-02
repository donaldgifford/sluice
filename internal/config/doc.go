// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

// Package config loads and validates the sluice manifest: the HCL
// declaration of the mirror bucket and the approved provider versions.
//
// The package exposes two entry points, [LoadDir] and [LoadFile], both
// returning a fully validated [Manifest]. There is no way to obtain a
// decoded-but-unvalidated manifest; every command consumes the same
// parse-and-validate path that `sluice validate` exercises.
//
// Failures surface as one of two error types sharing a one-line-per-
// failure rendering contract: [DiagnosticsError] for parse and decode
// problems (position-aware, produced by hclkit), and [ValidationError]
// for semantic rule violations (addressed by block label).
package config
