// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/registry"
)

// policyFetcher adapts the registry client to publish.Fetcher, adding
// the per-provider verification policy the manifest declares. The
// publisher deliberately knows nothing about signing-key policy —
// translating a manifest into one belongs here, next to the manifest.
type policyFetcher struct {
	client *registry.Client

	// allowExpired is keyed by normalized provider source. A source
	// absent from the map gets the strict default, so a provider the
	// manifest never declared can never inherit a relaxation.
	allowExpired map[string]bool

	// refreshedKeys are the armored exports read from
	// mirror.signing_key_files, read once here rather than per fetch.
	refreshedKeys []string
}

// newPolicyFetcher builds the fetcher for one manifest, reading any
// refreshed signing-key exports up front so a missing or unreadable
// file fails before the first network call rather than mid-apply.
func newPolicyFetcher(m *config.Manifest) (*policyFetcher, error) {
	allow := make(map[string]bool, len(m.Providers))
	for _, p := range m.Providers {
		if p.AllowExpiredSigningKey {
			allow[p.Source] = true
		}
	}

	keys := make([]string, 0, len(m.Mirror.SigningKeyFiles))
	for _, path := range m.Mirror.SigningKeyFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading mirror.signing_key_files entry %q: %w", path, err)
		}
		keys = append(keys, string(data))
	}
	return &policyFetcher{client: registry.New(), allowExpired: allow, refreshedKeys: keys}, nil
}

func (f *policyFetcher) FetchVerified(
	ctx context.Context, source, version string, platform config.Platform, destDir string,
) (*registry.Artifact, error) {
	return f.client.FetchVerified(ctx, &registry.FetchRequest{
		Source:                 source,
		Version:                version,
		Platform:               platform,
		DestDir:                destDir,
		RefreshedSigningKeys:   f.refreshedKeys,
		AllowExpiredSigningKey: f.allowExpired[source],
	})
}
