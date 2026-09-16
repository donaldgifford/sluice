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
//
// The manifest also carries the two signing-key policy knobs
// [Mirror.SigningKeyFiles] and [Provider.AllowExpiredSigningKey]. This
// package only models and validates them; interpreting them belongs to
// the verification chain in internal/registry.
//
// The mirror block further carries the S3-backend selection
// ([Mirror.Endpoint], [Mirror.PathStyle]): an http(s) base URL selects
// an S3-compatible backend with mandatory path-style addressing, empty
// means AWS. CLI-layer flag and environment overrides resolve onto
// these fields after load (cmd/sluice), so validation here covers the
// HCL-authored pair only.
package config
