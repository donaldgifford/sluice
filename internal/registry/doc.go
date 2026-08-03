// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

// Package registry fetches provider release artifacts from an origin
// registry (registry.terraform.io, registry.opentofu.org, or anything
// speaking the same v1 provider protocol) and verifies them before
// anything is staged for publication.
//
// The package exports one orchestration entry point,
// [Client.FetchVerified], which runs the full chain for a single
// (provider, version, platform) tuple:
//
//  1. resolve download metadata from
//     /v1/providers/{ns}/{type}/{version}/download/{os}/{arch};
//  2. verify SHA256SUMS against its detached GPG signature using only
//     the keys the registry published in that response;
//  3. stream the zip to a temp file, size-bounded and context-aware,
//     computing its SHA-256 during the stream;
//  4. compare the streamed SHA-256 to the signed sums entry and
//     compute the "h1:" dirhash.
//
// Only after every step passes is the zip renamed into the staging
// directory; any failure leaves the directory untouched. The
// intermediate steps are deliberately unexported so callers cannot
// compose them in a weaker order.
//
// Registries embed a copy of the signing key as it stood when a
// provider version was published and do not re-cut it when the owner
// later extends the key, so a published key can report an expiry its
// real key material has moved past.
// [FetchRequest.RefreshedSigningKeys] supplies current exports, which
// replace the registry's copy only on a full primary-fingerprint
// match: the trusted set stays exactly what the registry published,
// with fresher validity metadata and nothing relaxed.
// [FetchRequest.AllowExpiredSigningKey] is the narrower escape hatch
// for when no refreshed export exists — it relaxes expiry alone, still
// refusing revoked keys (re-checked against the present, not the
// signing time), bad signatures, unknown signers, and future-dated
// signatures.
//
// Trust never derives from transport origin — registries legitimately
// serve zips from unrelated CDN hosts — but every fetched URL must be
// https in production. Failures carry the (provider, version,
// platform) tuple via [Error] and classify via the Err* sentinels.
package registry
