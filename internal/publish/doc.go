// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

// Package publish owns the mirror's write path: reading actual state
// from the bucket, applying a plan, signing artifacts, and emitting
// the audit trail.
//
// The write path is ordered, conditional at the index, idempotent,
// signed, and audited:
//
//   - Per provider, objects are written zips → <version>.json →
//     index.json last. The index is the sole commit point: a version
//     is not published until the index references it, so a crash at
//     any earlier step leaves the mirror's observable state unchanged
//     and a rerun converges.
//   - The index write is conditional (If-Match on the ETag captured
//     when actual state was read; If-None-Match for a first publish).
//     A concurrent writer surfaces as [ErrConflict] — exit 3, re-plan.
//   - Removal rewrites the index without the version first, then
//     deletes <version>.json. Zips are never deleted (forensics).
//   - Every artifact is cosign-signed and carries an in-toto
//     attestation before upload; a missing or wrong-version cosign
//     fails the apply before anything is fetched or written.
//
// Bucket reads follow the protocol caveat in [internal/mirror]:
// provider discovery LISTs the bucket (an index absent from the
// manifest must still be seen for removal to work); content is then
// read only from the discovered indexes and the version documents
// they reference.
package publish
