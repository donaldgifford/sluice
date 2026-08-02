// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package hash

import (
	"fmt"
	"path/filepath"

	"golang.org/x/mod/sumdb/dirhash"
)

// Zip returns the "h1:" dirhash of the provider zip at path — the
// value Terraform records in .terraform.lock.hcl for the archive's
// unpacked contents.
func Zip(path string) (string, error) {
	h, err := dirhash.HashZip(path, dirhash.Hash1)
	if err != nil {
		return "", fmt.Errorf("h1 hash %s: %w", filepath.Base(path), err)
	}
	return h, nil
}
