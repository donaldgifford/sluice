// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Platform is one os_arch cell of the mirror matrix.
type Platform struct {
	OS   string
	Arch string
}

// String returns the canonical os_arch form, e.g. "linux_amd64".
func (p Platform) String() string { return p.OS + "_" + p.Arch }

// knownPlatforms is the curated os_arch matrix. Extend here and only
// here; validation, the diff engine, and registry downloads all draw
// from this map.
var knownPlatforms = map[string]Platform{
	"darwin_amd64":  {OS: "darwin", Arch: "amd64"},
	"darwin_arm64":  {OS: "darwin", Arch: "arm64"},
	"linux_amd64":   {OS: "linux", Arch: "amd64"},
	"linux_arm64":   {OS: "linux", Arch: "arm64"},
	"windows_amd64": {OS: "windows", Arch: "amd64"},
}

// ParsePlatform maps an os_arch string to its Platform. Anything
// outside the curated matrix is an error naming the known set.
func ParsePlatform(s string) (Platform, error) {
	p, ok := knownPlatforms[s]
	if !ok {
		return Platform{}, fmt.Errorf(
			"unknown platform %q; known platforms: %s",
			s, strings.Join(knownPlatformNames(), ", "))
	}
	return p, nil
}

// KnownPlatforms returns the curated matrix sorted by os_arch name.
// The slice is a copy; callers may mutate it freely.
func KnownPlatforms() []Platform {
	names := knownPlatformNames()
	ps := make([]Platform, len(names))
	for i, name := range names {
		ps[i] = knownPlatforms[name]
	}
	return ps
}

func knownPlatformNames() []string {
	return slices.Sorted(maps.Keys(knownPlatforms))
}
