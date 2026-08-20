// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

// Package mirror holds the provider network mirror protocol types and
// the pure diff engine at the heart of plan/apply.
//
// [State] is the diffable shape shared by both sides: the desired
// state expanded from a validated manifest via [Desired], and the
// actual state a bucket reader produces from the mirror's own
// protocol files ([Index], [VersionDoc]). [Diff] reconciles the two
// into a [Plan] of exactly three action kinds: add a version, remove
// a version, add platforms to an existing version.
//
// Two asymmetries are deliberate. There is no remove-platform action:
// a shrunk platform list produces no diff, and the mirror keeps
// serving what it already has. And removal is driven by the actual
// side — any provider present on the mirror but absent from the
// manifest is removed wholesale — so the bucket reader must list the
// bucket, not merely fetch the indexes the manifest declares.
package mirror
