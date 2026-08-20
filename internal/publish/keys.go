// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"regexp"
	"strings"
)

// Object-key derivation. A provider source address
// ("hostname/namespace/type") maps 1:1 to its bucket prefix, so the
// mirror layout is exactly the network-mirror protocol layout.

func indexKey(source string) string {
	return source + "/index.json"
}

func versionKey(source, version string) string {
	return source + "/" + version + ".json"
}

func zipKey(source, filename string) string {
	return source + "/" + filename
}

// indexKeyRE matches exactly a provider index at the protocol depth:
// hostname/namespace/type/index.json. Anything else under a prefix —
// zips, signatures, attestations, odd depths — is an artifact, not
// state.
var indexKeyRE = regexp.MustCompile(`^[^/]+/[^/]+/[^/]+/index\.json$`)

// discoverSources extracts provider source addresses from a full key
// listing by finding index.json objects at the protocol depth. LIST
// is the discovery mechanism on purpose: an index absent from the
// manifest must still be seen, or whole-provider removal could never
// be planned.
func discoverSources(keys []string) []string {
	var sources []string
	for _, k := range keys {
		if indexKeyRE.MatchString(k) {
			sources = append(sources, strings.TrimSuffix(k, "/index.json"))
		}
	}
	return sources
}
