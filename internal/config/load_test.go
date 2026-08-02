// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadDirValid(t *testing.T) {
	t.Parallel()

	m, err := LoadDir("testdata/valid")
	if err != nil {
		t.Fatalf("LoadDir(valid) unexpected error: %v", err)
	}

	want := Mirror{
		Bucket: "org-tf-mirror",
		Region: "us-east-1",
		Platforms: []Platform{
			{OS: "linux", Arch: "amd64"},
			{OS: "darwin", Arch: "arm64"},
		},
	}
	if m.Mirror.Bucket != want.Bucket || m.Mirror.Region != want.Region {
		t.Fatalf("Mirror = %+v, want %+v", m.Mirror, want)
	}
	if len(m.Mirror.Platforms) != 2 || m.Mirror.Platforms[0] != want.Platforms[0] || m.Mirror.Platforms[1] != want.Platforms[1] {
		t.Fatalf("Mirror.Platforms = %v, want %v", m.Mirror.Platforms, want.Platforms)
	}

	// Declaration order: lexical file order (cloudflare.hcl,
	// hashicorp.hcl, opentofu.hcl), then in-file order.
	wantSources := []string{
		"registry.terraform.io/cloudflare/cloudflare",
		"registry.terraform.io/hashicorp/aws",
		"registry.opentofu.org/hashicorp/null",
	}
	if len(m.Providers) != len(wantSources) {
		t.Fatalf("got %d providers, want %d: %+v", len(m.Providers), len(wantSources), m.Providers)
	}
	for i, src := range wantSources {
		if m.Providers[i].Source != src {
			t.Fatalf("Providers[%d].Source = %q, want %q", i, m.Providers[i].Source, src)
		}
	}

	// cloudflare overrides the matrix; the others inherit it.
	if got := m.Providers[0].Platforms; len(got) != 1 || got[0] != (Platform{OS: "linux", Arch: "amd64"}) {
		t.Fatalf("override Platforms = %v, want [linux_amd64]", got)
	}
	if got := m.Providers[1].Platforms; len(got) != 2 {
		t.Fatalf("inherited Platforms = %v, want the 2-entry mirror matrix", got)
	}
	if got := m.Providers[1].Versions; len(got) != 2 || got[0] != "6.2.0" || got[1] != "6.3.0" {
		t.Fatalf("aws Versions = %v, want [6.2.0 6.3.0]", got)
	}
}

func TestLoadFileValid(t *testing.T) {
	t.Parallel()

	m, err := LoadFile("testdata/valid-single.hcl")
	if err != nil {
		t.Fatalf("LoadFile(valid-single) unexpected error: %v", err)
	}
	if m.Mirror.Bucket != "org-tf-mirror" || len(m.Providers) != 1 {
		t.Fatalf("unexpected manifest: %+v", m)
	}
}

func TestLoadDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string // substrings that must appear in Error()
	}{
		{
			name: "missing mirror block",
			src: `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0"]
}
`,
			want: []string{"mirror", "block"},
		},
		{
			name: "duplicate mirror block",
			src: `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = ["linux_amd64"]
}

mirror {
  bucket    = "b"
  region    = "us-east-1"
  platforms = ["linux_amd64"]
}
`,
			want: []string{"Duplicate", "mirror", "test.hcl:7"},
		},
		{
			name: "missing bucket",
			src: `mirror {
  region    = "us-east-1"
  platforms = ["linux_amd64"]
}
`,
			want: []string{"Missing required argument", `"bucket"`},
		},
		{
			name: "missing region",
			src: `mirror {
  bucket    = "a"
  platforms = ["linux_amd64"]
}
`,
			want: []string{"Missing required argument", `"region"`},
		},
		{
			name: "missing mirror platforms",
			src: `mirror {
  bucket = "a"
  region = "us-east-1"
}
`,
			want: []string{"Missing required argument", `"platforms"`},
		},
		{
			name: "missing versions",
			src: `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = ["linux_amd64"]
}

provider "registry.terraform.io/hashicorp/aws" {
}
`,
			want: []string{"Missing required argument", `"versions"`},
		},
		{
			name: "syntax error carries position",
			src: `mirror {
  bucket =
}
`,
			want: []string{"test.hcl:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadBytes("test.hcl", []byte(tt.src))
			if err == nil {
				t.Fatal("expected an error, got none")
			}

			var diagErr *DiagnosticsError
			if !errors.As(err, &diagErr) {
				t.Fatalf("error type = %T, want *DiagnosticsError; err: %v", err, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestLoadDirDuplicateMirrorAcrossFiles(t *testing.T) {
	t.Parallel()

	_, err := LoadDir("testdata/dup-mirror-across-files")
	if err == nil {
		t.Fatal("expected an error, got none")
	}

	var diagErr *DiagnosticsError
	if !errors.As(err, &diagErr) {
		t.Fatalf("error type = %T, want *DiagnosticsError; err: %v", err, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "Duplicate") || !strings.Contains(msg, "mirror") {
		t.Fatalf("error %q does not report the duplicate mirror block", msg)
	}
	// The duplicate is in the second merged file; the position must
	// say so.
	if !strings.Contains(msg, "b.hcl") {
		t.Fatalf("error %q does not point at the offending file", msg)
	}
}

func TestLoadDirEmpty(t *testing.T) {
	t.Parallel()

	_, err := LoadDir(t.TempDir())
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	var diagErr *DiagnosticsError
	if !errors.As(err, &diagErr) {
		t.Fatalf("error type = %T, want *DiagnosticsError; err: %v", err, err)
	}
	if !strings.Contains(err.Error(), "No HCL files") {
		t.Fatalf("error %q does not report the empty directory", err.Error())
	}
}

func TestLoadDirMissing(t *testing.T) {
	t.Parallel()

	_, err := LoadDir("testdata/does-not-exist")
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error %q does not name the missing directory", err.Error())
	}
}

func TestLoadFileMissing(t *testing.T) {
	t.Parallel()

	_, err := LoadFile("testdata/does-not-exist.hcl")
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	if !strings.Contains(err.Error(), "does-not-exist.hcl") {
		t.Fatalf("error %q does not name the missing file", err.Error())
	}
}
