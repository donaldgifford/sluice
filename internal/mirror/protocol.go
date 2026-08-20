// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package mirror

// Index is a provider's index.json: the set of mirrored versions.
//
//	{"versions":{"6.2.0":{},"6.3.0":{}}}
type Index struct {
	Versions map[string]IndexEntry `json:"versions"`
}

// IndexEntry is the per-version object in index.json — empty in the
// current protocol. A named type so the schema can grow without
// changing the map signature.
type IndexEntry struct{}

// VersionDoc is a provider's <version>.json: one archive per
// platform.
//
//	{"archives":{"linux_amd64":{"url":"...zip","hashes":["h1:..."]}}}
type VersionDoc struct {
	// Archives is keyed by canonical os_arch.
	Archives map[string]Archive `json:"archives"`
}

// Archive locates one platform's zip and the hashes Terraform
// validates against its lock file.
type Archive struct {
	URL    string   `json:"url"`
	Hashes []string `json:"hashes"`
}
