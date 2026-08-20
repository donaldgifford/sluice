// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package hash

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// makeZip builds a zip under t.TempDir() from a name → content map.
// dirhash.Hash1 hashes sorted entry names plus content SHA-256s only,
// so timestamps and insertion order never enter the h1 value.
func makeZip(t *testing.T, files map[string]string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fixture.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating zip: %v", err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("adding %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing zip file: %v", err)
	}
	return path
}

// The goldens pin the wiring — that Zip is HashZip with Hash1 and
// nothing ever swaps in HashDir, HashZipContents, or another hash
// function. dirhash IS the implementation Terraform uses for "h1:",
// so the goldens are not cross-implementation evidence; the proof
// that a sluice-populated mirror satisfies real lock files is the
// Phase 5 e2e (`terraform init` / `tofu init` against the mirror).
func TestZip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "single file",
			files: map[string]string{"terraform-provider-null_v3.2.4_x5": "fake provider binary\n"},
			want:  "h1:to0PkCkAztW0GsvRZ/tADCKt8V6/81Uy1eBW+sM6J2w=",
		},
		{
			name: "multiple files",
			files: map[string]string{
				"terraform-provider-aws_v6.3.0_x5": "binary bytes\n",
				"LICENSE.txt":                      "Apache-2.0\n",
				"README.md":                        "readme\n",
			},
			want: "h1:LQ2PTTrJ7PS6ynZ45Bem6r/OciAaEOmL7n1L21JO8hI=",
		},
		{
			name:  "empty zip",
			files: map[string]string{},
			want:  "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
		},
		{
			name:  "nested paths",
			files: map[string]string{"docs/index.md": "docs\n", "bin/provider": "bin\n"},
			want:  "h1:Rpfrjn6/oiIzwhe01BMGMJMxE1dskRLxMWtWvZ3rTg4=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Zip(makeZip(t, tt.files))
			if err != nil {
				t.Fatalf("Zip() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Zip() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestZipErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path func(t *testing.T) string
	}{
		{
			name: "missing file",
			path: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "absent.zip")
			},
		},
		{
			name: "not a zip",
			path: func(t *testing.T) string {
				t.Helper()
				p := filepath.Join(t.TempDir(), "junk.zip")
				if err := os.WriteFile(p, []byte("not a zip archive"), 0o644); err != nil {
					t.Fatalf("writing junk: %v", err)
				}
				return p
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Zip(tt.path(t)); err == nil {
				t.Fatal("Zip() error = nil, want non-nil")
			}
		})
	}
}
