// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package bootstrap

import (
	"fmt"
	"strings"

	version "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"

	"github.com/donaldgifford/hclkit/pkg/hclkit"
)

// lockHCL is a lenient decode target for .terraform.lock.hcl: the
// remain fields absorb constraints, hashes, and anything future
// Terraform versions add.
type lockHCL struct {
	Providers []lockProviderHCL `hcl:"provider,block"`
	Remain    hcl.Body          `hcl:",remain"`
}

type lockProviderHCL struct {
	Source  string   `hcl:"source,label"`
	Version string   `hcl:"version"`
	Remain  hcl.Body `hcl:",remain"`
}

// entry is one (provider, version) pair from a lock file, normalized:
// lowercase address, go-version-normalized version string.
type entry struct {
	source  string
	version string
}

// parseLock reads one lock file. A malformed file is a loud, wrapped
// error carrying its path — bootstrap never skips silently.
func parseLock(path string) ([]entry, error) {
	var raw lockHCL
	if diags := hclkit.New().LoadFile(path, &raw); diags.HasErrors() {
		return nil, fmt.Errorf("parsing %s: %w", path, error(diags))
	}

	entries := make([]entry, 0, len(raw.Providers))
	for _, p := range raw.Providers {
		v, err := version.NewVersion(p.Version)
		if err != nil {
			return nil, fmt.Errorf("%s: provider %s: version %q: %w",
				path, p.Source, p.Version, err)
		}
		entries = append(entries, entry{
			source:  strings.ToLower(p.Source),
			version: v.String(),
		})
	}
	return entries, nil
}
