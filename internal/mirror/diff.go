// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package mirror

import (
	"fmt"
	"slices"
	"strings"

	version "github.com/hashicorp/go-version"
)

// Plan is the reconciliation result. The three action kinds are
// encoded structurally — a fourth kind cannot exist by construction —
// and the struct marshals directly to the plan --json contract. All
// slices are non-nil so empty categories marshal as [], never null.
type Plan struct {
	Add         []VersionChange `json:"add"`
	Remove      []VersionRef    `json:"remove"`
	AddPlatform []VersionChange `json:"add_platform"`
}

// VersionChange is one version gaining platforms: the full set for an
// Add, only the missing ones for an AddPlatform.
type VersionChange struct {
	Provider  string   `json:"provider"`
	Version   string   `json:"version"`
	Platforms []string `json:"platforms"`
}

// VersionRef names a version leaving the mirror index.
type VersionRef struct {
	Provider string `json:"provider"`
	Version  string `json:"version"`
}

// Empty reports whether the plan changes nothing; it drives plan's
// --detailed-exitcode contract.
func (p *Plan) Empty() bool {
	return len(p.Add) == 0 && len(p.Remove) == 0 && len(p.AddPlatform) == 0
}

// Diff reconciles desired against actual. Ordering is deterministic:
// providers lexically (addresses are normalized lowercase), versions
// version-aware (6.9.0 before 6.10.0, prereleases before their
// release, matching Terraform), platforms lexically within an action.
//
// The only error is a version key that does not parse as an exact
// version — on the actual side that means a corrupt mirror index;
// desired keys cannot fail because config normalized them.
func Diff(desired, actual State) (*Plan, error) {
	plan := &Plan{
		Add:         []VersionChange{},
		Remove:      []VersionRef{},
		AddPlatform: []VersionChange{},
	}

	for _, provider := range sortedKeys(desired) {
		versions, err := sortedVersionKeys(provider, desired[provider])
		if err != nil {
			return nil, err
		}
		actualVersions := actual[provider]
		for _, ver := range versions {
			wantPlatforms := desired[provider][ver]

			actualPlatforms, exists := actualVersions[ver]
			if !exists {
				plan.Add = append(plan.Add, VersionChange{
					Provider:  provider,
					Version:   ver,
					Platforms: sortedSet(wantPlatforms),
				})
				continue
			}
			if missing := missingFrom(wantPlatforms, actualPlatforms); len(missing) > 0 {
				plan.AddPlatform = append(plan.AddPlatform, VersionChange{
					Provider:  provider,
					Version:   ver,
					Platforms: missing,
				})
			}
			// Platforms present in actual but not desired produce no
			// action: there is no remove-platform kind, by design.
		}
	}

	for _, provider := range sortedKeys(actual) {
		versions, err := sortedVersionKeys(provider, actual[provider])
		if err != nil {
			return nil, err
		}
		desiredVersions := desired[provider]
		for _, ver := range versions {
			if _, keep := desiredVersions[ver]; !keep {
				plan.Remove = append(plan.Remove, VersionRef{
					Provider: provider,
					Version:  ver,
				})
			}
		}
	}

	return plan, nil
}

// sortedKeys returns the provider addresses in lexical order.
func sortedKeys(s State) []string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// sortedVersionKeys returns a provider's version keys in
// version-aware order, erroring on any key that is not an exact
// version.
func sortedVersionKeys(provider string, ps ProviderState) ([]string, error) {
	keys := make([]string, 0, len(ps))
	parsed := make(map[string]*version.Version, len(ps))
	for k := range ps {
		v, err := version.NewVersion(k)
		if err != nil {
			return nil, fmt.Errorf("provider %s: sorting version %q: %w", provider, k, err)
		}
		parsed[k] = v
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if c := parsed[a].Compare(parsed[b]); c != 0 {
			return c
		}
		// Distinct spellings of an equal version tie-break lexically
		// so the order stays total.
		return strings.Compare(a, b)
	})
	return keys, nil
}

// sortedSet returns the platform set as a lexically sorted slice.
func sortedSet(set PlatformSet) []string {
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// missingFrom returns the members of want absent from have, sorted.
func missingFrom(want, have PlatformSet) []string {
	var out []string
	for p := range want {
		if _, ok := have[p]; !ok {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}
