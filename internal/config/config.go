// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

// Manifest is the validated model of the approved-providers config:
// one mirror declaration plus the providers approved for it. It is
// plain data — no HCL tags — and safe to hand to the diff engine and
// exporters without further checks.
type Manifest struct {
	Mirror Mirror

	// Providers preserves declaration order: lexical file order under
	// LoadDir, then in-file order.
	Providers []Provider
}

// Mirror describes the target bucket and the default platform matrix
// every provider inherits unless it overrides.
type Mirror struct {
	Bucket string
	Region string

	Platforms []Platform

	// SigningKeyFiles are paths to armored public-key exports that
	// replace a registry's copy of the same key, matched by primary
	// fingerprint. Registries embed a key export when a provider is
	// published and do not re-cut it when the owner later extends the
	// key, so their copy can report an expiry the real key material
	// moved past. Paths are resolved relative to the process working
	// directory.
	SigningKeyFiles []string
}

// Provider is one approved provider block, fully resolved: Platforms
// is always non-empty after a successful load (the block's override if
// present, otherwise the mirror default).
type Provider struct {
	// Source is the normalized full source address,
	// "hostname/namespace/type" with a lowercase hostname, e.g.
	// "registry.terraform.io/hashicorp/aws".
	Source string

	// Versions are exact, normalized version strings in declaration
	// order.
	Versions []string

	Platforms []Platform

	// AllowExpiredSigningKey accepts a SHA256SUMS signature whose
	// registry-published key has since expired, provided the key was
	// valid when the signature was made. Off by default: expiry is a
	// deliberate signal from the key's owner, so overriding it is a
	// per-provider decision that has to be written down and reviewed.
	// Revoked keys are still refused, override or not.
	AllowExpiredSigningKey bool
}
