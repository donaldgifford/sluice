// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package bootstrap

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFindLockFiles(t *testing.T) {
	t.Parallel()

	paths, err := findLockFiles(filepath.Join("testdata", "tree"))
	if err != nil {
		t.Fatalf("findLockFiles() unexpected error: %v", err)
	}

	if len(paths) != 3 {
		t.Fatalf("found %d lock files, want 3: %v", len(paths), paths)
	}
	for _, p := range paths {
		if strings.Contains(p, ".terraform"+string(filepath.Separator)) {
			t.Fatalf("walker descended into .terraform: %s", p)
		}
	}
}

func TestFindLockFilesDotRoot(t *testing.T) {
	t.Parallel()

	// A root that is itself a dot-directory must still be walked —
	// only nested dot-directories are skipped.
	paths, err := findLockFiles(filepath.Join("testdata", "tree", "repo-a", ".terraform"))
	if err != nil {
		t.Fatalf("findLockFiles() unexpected error: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("found %d lock files under the dot root, want 1: %v", len(paths), paths)
	}
}
