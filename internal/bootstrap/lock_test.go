// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLock(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), lockFileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing lock fixture: %v", err)
	}
	return path
}

func TestParseLock(t *testing.T) {
	t.Parallel()

	path := writeLock(t, `provider "Registry.Terraform.IO/HashiCorp/AWS" {
  version     = "6.2"
  constraints = "~> 6.0"
  hashes      = ["h1:abc="]
}
`)
	entries, err := parseLock(path)
	if err != nil {
		t.Fatalf("parseLock() unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want 1", entries)
	}
	// Address lowercased, version normalized — same intake rules as
	// the manifest itself.
	want := entry{source: "registry.terraform.io/hashicorp/aws", version: "6.2.0"}
	if entries[0] != want {
		t.Fatalf("entry = %+v, want %+v", entries[0], want)
	}
}

func TestParseLockUnknownAttributesTolerated(t *testing.T) {
	t.Parallel()

	// Future Terraform additions must not break parsing.
	path := writeLock(t, `provider "registry.terraform.io/hashicorp/aws" {
  version         = "6.2.0"
  constraints     = "~> 6.0"
  hashes          = ["h1:abc="]
  future_attr     = "tolerated"
}

future_block "x" {
  anything = true
}
`)
	entries, err := parseLock(path)
	if err != nil {
		t.Fatalf("parseLock() unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want 1", entries)
	}
}

func TestParseLockMalformed(t *testing.T) {
	t.Parallel()

	path := writeLock(t, `provider "registry.terraform.io/hashicorp/aws" {
  version =
}
`)
	_, err := parseLock(path)
	if err == nil {
		t.Fatal("expected an error for a malformed lock file")
	}
	if !strings.Contains(err.Error(), lockFileName) {
		t.Fatalf("error %q does not carry the lock file path", err)
	}
}

func TestParseLockBadVersion(t *testing.T) {
	t.Parallel()

	path := writeLock(t, `provider "registry.terraform.io/hashicorp/aws" {
  version = "not-a-version"
}
`)
	_, err := parseLock(path)
	if err == nil {
		t.Fatal("expected an error for an unparseable version")
	}
	for _, want := range []string{"registry.terraform.io/hashicorp/aws", `"not-a-version"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}
