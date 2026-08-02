// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package bootstrap

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

const lockFileName = ".terraform.lock.hcl"

// findLockFiles walks root and returns every .terraform.lock.hcl in
// lexical walk order. Dot-directories under the root are skipped —
// one rule covering .git and .terraform, whose modules/ subtree
// carries third-party repos' own lock files that are not "in use
// here". Symlinked directories are not followed (filepath.WalkDir's
// native behavior).
func findLockFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() == lockFileName {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	return paths, nil
}
