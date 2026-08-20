// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package bootstrap

import (
	"fmt"
	"os"
)

// Generate walks each root for .terraform.lock.hcl files and renders
// a complete manifest seeded from every (provider, version) pair in
// use, deduplicated across repos. The output passes `sluice validate`
// unmodified. Overlapping roots are harmless: aggregation is
// idempotent.
func Generate(roots []string) ([]byte, error) {
	providers := make(map[string]map[string]struct{})

	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			return nil, fmt.Errorf("bootstrap root: %w", err)
		}
		paths, err := findLockFiles(root)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			entries, err := parseLock(path)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if providers[e.source] == nil {
					providers[e.source] = make(map[string]struct{})
				}
				providers[e.source][e.version] = struct{}{}
			}
		}
	}

	return render(providers), nil
}
