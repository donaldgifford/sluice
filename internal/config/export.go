// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	version "github.com/hashicorp/go-version"
)

// exportDoc is the canonical policy-repo projection:
// {"providers":{"<addr>":["<version>",...]}}.
type exportDoc struct {
	Providers map[string][]string `json:"providers"`
}

// ExportJSON renders the manifest's approved set as canonical JSON:
// provider addresses sorted (encoding/json sorts map keys), versions
// version-aware sorted (6.9.0 before 6.10.0, prereleases before their
// release), two-space indented, trailing newline. The bytes are
// deterministic — the policy repo commits them as data/providers.json
// and diffs them with a --check-style comparison.
//
// The error path exists only for the defensive version re-parse; it
// cannot fire on a Manifest produced by Load.
func ExportJSON(m *Manifest) ([]byte, error) {
	doc := exportDoc{Providers: make(map[string][]string, len(m.Providers))}
	for _, p := range m.Providers {
		versions := slices.Clone(p.Versions)
		if err := sortVersions(p.Source, versions); err != nil {
			return nil, err
		}
		doc.Providers[p.Source] = versions
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling export: %w", err)
	}
	return append(out, '\n'), nil
}

// sortVersions orders normalized version strings version-aware, in
// place.
func sortVersions(source string, versions []string) error {
	parsed := make(map[string]*version.Version, len(versions))
	for _, s := range versions {
		v, err := version.NewVersion(s)
		if err != nil {
			return fmt.Errorf("provider %s: sorting version %q: %w", source, s, err)
		}
		parsed[s] = v
	}
	slices.SortFunc(versions, func(a, b string) int {
		if c := parsed[a].Compare(parsed[b]); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	return nil
}
