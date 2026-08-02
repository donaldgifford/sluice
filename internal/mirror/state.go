// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package mirror

import (
	"github.com/donaldgifford/sluice/internal/config"
)

// State maps provider source address → version → platform set. It is
// the diffable shape shared by desired state ([Desired]) and the
// actual state a bucket reader assembles.
//
// Version keys must be exact go-version-parseable versions. Desired
// guarantees this by construction; a bucket reader must surface
// whatever the mirror actually contains and let [Diff] report the
// corruption.
type State map[string]ProviderState

// ProviderState maps version → platform set for one provider.
type ProviderState map[string]PlatformSet

// PlatformSet is keyed by canonical os_arch ("linux_amd64"). Keys are
// strings rather than config.Platform so actual state can carry
// platforms outside today's curated matrix (historical publishes)
// without breaking the diff.
type PlatformSet map[string]struct{}

// Desired expands a validated manifest into diffable state. Provider
// platform overrides are already resolved by config, so every
// (version, platform) tuple is explicit here.
func Desired(m *config.Manifest) State {
	s := make(State, len(m.Providers))
	for _, p := range m.Providers {
		ps := make(ProviderState, len(p.Versions))
		for _, v := range p.Versions {
			set := make(PlatformSet, len(p.Platforms))
			for _, plat := range p.Platforms {
				set[plat.String()] = struct{}{}
			}
			ps[v] = set
		}
		s[p.Source] = ps
	}
	return s
}
