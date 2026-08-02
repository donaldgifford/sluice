// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

// Package hash computes the "h1:" dirhash of provider zip archives —
// the scheme Terraform and OpenTofu record in .terraform.lock.hcl and
// verify on init. It is a thin wrapper over
// golang.org/x/mod/sumdb/dirhash, which is the same implementation the
// tools themselves use, so values produced here are bit-identical to
// what a client will accept from the mirror.
package hash
