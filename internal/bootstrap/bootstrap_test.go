// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package bootstrap

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/sluice/internal/config"
)

func TestGenerateGolden(t *testing.T) {
	t.Parallel()

	got, err := Generate([]string{filepath.Join("testdata", "tree")})
	if err != nil {
		t.Fatalf("Generate() unexpected error: %v", err)
	}

	golden := filepath.Join("testdata", "golden", "manifest.hcl")
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("manifest drifted from %s:\ngot:\n%s\nwant:\n%s", golden, got, want)
	}
}

func TestGenerateSemantics(t *testing.T) {
	t.Parallel()

	got, err := Generate([]string{filepath.Join("testdata", "tree")})
	if err != nil {
		t.Fatalf("Generate() unexpected error: %v", err)
	}
	out := string(got)

	// Dedupe: aws 6.2.0 appears in two repos but once in the output.
	if strings.Count(out, `"6.2.0"`) != 1 {
		t.Fatalf("aws 6.2.0 not deduplicated:\n%s", out)
	}
	// The .terraform decoy lock file must be skipped.
	if strings.Contains(out, "never") {
		t.Fatalf("walker descended into .terraform:\n%s", out)
	}
	// Both aws versions collected across repos, version-sorted.
	if !strings.Contains(out, `versions = ["6.2.0", "6.3.0"]`) {
		t.Fatalf("aws versions not aggregated and sorted:\n%s", out)
	}
}

func TestGenerateOverlappingRootsIdempotent(t *testing.T) {
	t.Parallel()

	tree := filepath.Join("testdata", "tree")
	once, err := Generate([]string{tree})
	if err != nil {
		t.Fatalf("Generate() unexpected error: %v", err)
	}
	twice, err := Generate([]string{tree, tree, filepath.Join(tree, "repo-a")})
	if err != nil {
		t.Fatalf("Generate() with overlapping roots: %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatal("overlapping roots changed the output; aggregation is not idempotent")
	}
}

func TestGenerateRoundTripsThroughValidate(t *testing.T) {
	t.Parallel()

	got, err := Generate([]string{filepath.Join("testdata", "tree")})
	if err != nil {
		t.Fatalf("Generate() unexpected error: %v", err)
	}

	path := filepath.Join(t.TempDir(), "manifest.hcl")
	if err := os.WriteFile(path, got, 0o644); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	if _, err := config.LoadFile(path); err != nil {
		t.Fatalf("bootstrap output does not validate:\n%v\n---\n%s", err, got)
	}
}

func TestGenerateMissingRoot(t *testing.T) {
	t.Parallel()

	_, err := Generate([]string{filepath.Join("testdata", "does-not-exist")})
	if err == nil {
		t.Fatal("expected an error for a missing root")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error %q does not name the missing root", err)
	}
}

func TestGenerateEmptyTree(t *testing.T) {
	t.Parallel()

	got, err := Generate([]string{t.TempDir()})
	if err != nil {
		t.Fatalf("Generate() on an empty tree: %v", err)
	}
	// No lock files → a valid manifest with just the mirror block.
	if strings.Contains(string(got), "provider ") {
		t.Fatalf("empty tree produced provider blocks:\n%s", got)
	}
	path := filepath.Join(t.TempDir(), "manifest.hcl")
	if err := os.WriteFile(path, got, 0o644); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	if _, err := config.LoadFile(path); err != nil {
		t.Fatalf("empty-tree output does not validate: %v", err)
	}
}
