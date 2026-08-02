// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

import (
	"errors"
	"strings"
	"testing"
)

// assertValidationError fails the test unless err is a
// *ValidationError whose rendering contains every wanted substring.
func assertValidationError(t *testing.T, err error, want []string) {
	t.Helper()

	if err == nil {
		t.Fatal("expected an error, got none")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("error type = %T, want *ValidationError; err: %v", err, err)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("error %q does not contain %q", err.Error(), w)
		}
	}
}

func TestDuplicateProviderLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "duplicate label in one file",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.2.0"]
}

provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0"]
}
`,
			want: []string{
				`provider "registry.terraform.io/hashicorp/aws": declared more than once`,
				"consolidate into one block",
			},
		},
		{
			name: "triplicate reports each repeat",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.1.0"]
}

provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.2.0"]
}

provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0"]
}
`,
			want: []string{"declared more than once"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadBytes("test.hcl", []byte(tt.src))
			assertValidationError(t, err, tt.want)
		})
	}
}

func TestDuplicateProviderLabelsPassCases(t *testing.T) {
	t.Parallel()

	// Distinct labels — including the same namespace/type on two
	// registries — are not duplicates.
	src := header + `provider "registry.terraform.io/hashicorp/null" {
  versions = ["3.2.4"]
}

provider "registry.opentofu.org/hashicorp/null" {
  versions = ["3.2.4"]
}
`
	m, err := loadBytes("test.hcl", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Providers) != 2 {
		t.Fatalf("got %d providers, want 2", len(m.Providers))
	}
}

func TestDuplicateProviderLabelsAcrossFiles(t *testing.T) {
	t.Parallel()

	_, err := LoadDir("testdata/dup-provider-across-files")
	assertValidationError(t, err, []string{
		`provider "registry.terraform.io/hashicorp/aws": declared more than once`,
	})
}
