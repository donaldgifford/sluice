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
}
