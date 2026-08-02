// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// exportGolden loads a manifest from inline HCL, exports it, and
// compares against the named golden file.
func exportGolden(t *testing.T, src, golden string) {
	t.Helper()

	m, err := loadBytes("test.hcl", []byte(src))
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	got, err := ExportJSON(m)
	if err != nil {
		t.Fatalf("ExportJSON() unexpected error: %v", err)
	}

	path := filepath.Join("testdata", "golden", golden)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("export drifted from %s:\ngot:\n%s\nwant:\n%s", path, got, want)
	}

	// Byte determinism: a second run over the same manifest must be
	// identical.
	again, err := ExportJSON(m)
	if err != nil {
		t.Fatalf("ExportJSON() second run error: %v", err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("ExportJSON() is not byte-deterministic across runs")
	}
}

func TestExportJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		src    string
		golden string
	}{
		{
			name: "basic",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.2.0", "6.3.0"]
}

provider "registry.opentofu.org/hashicorp/null" {
  versions = ["3.2.4"]
}
`,
			golden: "export_basic.json",
		},
		{
			name:   "empty",
			src:    header,
			golden: "export_empty.json",
		},
		{
			// Declaration order deliberately violates both sort
			// properties: hashicorp before cloudflare, versions
			// shuffled with a 6.9/6.10 pair and a prerelease. The
			// golden proves keys sort lexically and versions sort
			// version-aware.
			name: "sorted",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["7.0.0", "6.10.0", "7.0.0-beta1", "6.9.0"]
}

provider "registry.terraform.io/cloudflare/cloudflare" {
  versions = ["5.4.0"]
}
`,
			golden: "export_sorted.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exportGolden(t, tt.src, tt.golden)
		})
	}
}

func TestExportJSONDoesNotMutateManifest(t *testing.T) {
	t.Parallel()

	src := header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0", "6.2.0"]
}
`
	m, err := loadBytes("test.hcl", []byte(src))
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	if _, err := ExportJSON(m); err != nil {
		t.Fatalf("ExportJSON() unexpected error: %v", err)
	}
	// The manifest keeps declaration order; export sorts a copy.
	if got := m.Providers[0].Versions; got[0] != "6.3.0" || got[1] != "6.2.0" {
		t.Fatalf("Versions = %v, want declaration order preserved", got)
	}
}
