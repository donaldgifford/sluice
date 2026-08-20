// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

// Package bootstrap seeds a sluice manifest from the provider
// versions a fleet already uses: it walks directory trees for
// .terraform.lock.hcl files, aggregates the exact (provider, version)
// pairs they record, and renders a complete manifest that passes
// `sluice validate` unmodified — including a placeholder mirror block
// the operator edits before first apply.
//
// Lock-file constraint and hash information is ignored on purpose:
// lock files record exact versions, which is exactly what sluice
// wants. Addresses are emitted as found (lowercased); if a lock file
// carries a source address sluice's validator rejects — a port-
// qualified or dotless hostname — bootstrap does not launder it, and
// validate reports the actionable error instead.
package bootstrap
