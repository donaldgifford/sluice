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

func TestSourceAddressValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "two segments",
			src: header + `provider "hashicorp/aws" {
  versions = ["6.3.0"]
}
`,
			want: []string{`provider "hashicorp/aws": invalid source address`, `"hostname/namespace/type"`},
		},
		{
			name: "four segments",
			src: header + `provider "registry.terraform.io/hashicorp/aws/extra" {
  versions = ["6.3.0"]
}
`,
			want: []string{"invalid source address"},
		},
		{
			name: "empty segment",
			src: header + `provider "registry.terraform.io//aws" {
  versions = ["6.3.0"]
}
`,
			want: []string{"invalid source address"},
		},
		{
			name: "hostname without a dot",
			src: header + `provider "localhost/hashicorp/aws" {
  versions = ["6.3.0"]
}
`,
			want: []string{"invalid source address"},
		},
		{
			name: "hostname with invalid characters",
			src: header + `provider "registry_internal.io/hashicorp/aws" {
  versions = ["6.3.0"]
}
`,
			want: []string{"invalid source address"},
		},
		{
			name: "hostname label with leading hyphen",
			src: header + `provider "-registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0"]
}
`,
			want: []string{"invalid source address"},
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

func TestSourceAddressNormalization(t *testing.T) {
	t.Parallel()

	src := header + `provider "Registry.Terraform.IO/HashiCorp/AWS" {
  versions = ["6.3.0"]
}
`
	m, err := loadBytes("test.hcl", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := m.Providers[0].Source; got != "registry.terraform.io/hashicorp/aws" {
		t.Fatalf("Source = %q, want the lowercase normalized form", got)
	}
}

func TestDuplicateLabelsDetectedAfterNormalization(t *testing.T) {
	t.Parallel()

	src := header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.2.0"]
}

provider "Registry.Terraform.IO/HashiCorp/AWS" {
  versions = ["6.3.0"]
}
`
	_, err := loadBytes("test.hcl", []byte(src))
	assertValidationError(t, err, []string{"declared more than once"})
}

func TestVersionValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "empty versions list",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = []
}
`,
			want: []string{"versions must not be empty", "delete its block"},
		},
		{
			name: "pessimistic constraint",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["~> 6.3"]
}
`,
			want: []string{
				`versions[0] "~> 6.3": version constraints are not supported`,
				"never resolves",
			},
		},
		{
			name: "range constraint",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = [">= 6.0, < 7.0"]
}
`,
			want: []string{"version constraints are not supported"},
		},
		{
			name: "garbage version",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["banana"]
}
`,
			want: []string{`versions[0] "banana" is not a valid version`},
		},
		{
			name: "v prefix rejected",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["v6.3.0"]
}
`,
			want: []string{`versions[0] "v6.3.0": registry versions are unprefixed`},
		},
		{
			name: "exact duplicate",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0", "6.3.0"]
}
`,
			want: []string{`duplicate version "6.3.0"`},
		},
		{
			name: "duplicate via normalization",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0", "6.3"]
}
`,
			want: []string{`duplicate version "6.3.0" ("6.3" normalizes to it)`},
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

func TestVersionPassCases(t *testing.T) {
	t.Parallel()

	src := header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0", "6.2.0", "3.2.0-beta1", "6.4"]
}
`
	m, err := loadBytes("test.hcl", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := m.Providers[0].Versions
	want := []string{"6.3.0", "6.2.0", "3.2.0-beta1", "6.4.0"}
	if len(got) != len(want) {
		t.Fatalf("Versions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Versions[%d] = %q, want %q (declaration order, normalized)", i, got[i], want[i])
		}
	}
}

func TestEmptyPlatformOverride(t *testing.T) {
	t.Parallel()

	src := header + `provider "registry.terraform.io/hashicorp/aws" {
  versions  = ["6.3.0"]
  platforms = []
}
`
	_, err := loadBytes("test.hcl", []byte(src))
	assertValidationError(t, err, []string{
		"platforms override must not be empty",
		"omit the attribute to inherit mirror.platforms",
	})
}

func TestEmptyMirrorPlatforms(t *testing.T) {
	t.Parallel()

	src := `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = []
}
`
	_, err := loadBytes("test.hcl", []byte(src))
	assertValidationError(t, err, []string{"mirror: platforms must not be empty"})
}

func TestAllRuleFamiliesReportTogether(t *testing.T) {
	t.Parallel()

	// One config violating label, version, and platform rules at once:
	// the semantic pass must surface all of them in a single run.
	src := `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = ["linux_riscv64"]
}

provider "hashicorp/aws" {
  versions = ["~> 6.3"]
}
`
	_, err := loadBytes("test.hcl", []byte(src))
	assertValidationError(t, err, []string{
		`unknown platform "linux_riscv64"`,
		"invalid source address",
		"version constraints are not supported",
	})
}

func TestMirrorEndpointValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "endpoint without path style",
			src: `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = ["linux_amd64"]
  endpoint  = "https://s3.internal"
}
`,
			want: []string{"mirror: path_style must be true when endpoint is set"},
		},
		{
			name: "non-URL endpoint",
			src: `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = ["linux_amd64"]
  endpoint  = "notaurl"
  path_style = true
}
`,
			want: []string{`mirror: endpoint "notaurl" is not a valid http(s) URL`},
		},
		{
			name: "non-http scheme endpoint",
			src: `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = ["linux_amd64"]
  endpoint  = "ftp://s3.internal/mirror"
  path_style = true
}
`,
			want: []string{`is not a valid http(s) URL`},
		},
		{
			name: "missing host endpoint",
			src: `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = ["linux_amd64"]
  endpoint  = "https://"
  path_style = true
}
`,
			want: []string{`is not a valid http(s) URL`},
		},
		{
			name: "invalid URL and missing path style report together",
			src: `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = ["linux_amd64"]
  endpoint  = "notaurl"
}
`,
			want: []string{
				`is not a valid http(s) URL`,
				"path_style must be true when endpoint is set",
			},
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

func TestMirrorEndpointPassCases(t *testing.T) {
	t.Parallel()

	src := `mirror {
  bucket     = "a"
  region     = "garage"
  platforms  = ["linux_amd64"]
  endpoint   = "https://s3.internal"
  path_style = true
}
`
	m, err := loadBytes("test.hcl", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Mirror.Endpoint != "https://s3.internal" {
		t.Fatalf("Endpoint = %q, want %q", m.Mirror.Endpoint, "https://s3.internal")
	}
	if !m.Mirror.PathStyle {
		t.Fatal("PathStyle = false, want true")
	}
}

func TestMirrorEndpointDefaultsEmpty(t *testing.T) {
	t.Parallel()

	// The shared header has no endpoint attributes: AWS path stays
	// zero-valued.
	m, err := loadBytes("test.hcl", []byte(header+`provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0"]
}
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Mirror.Endpoint != "" || m.Mirror.PathStyle {
		t.Fatalf("Endpoint/PathStyle = %q/%v, want zero values",
			m.Mirror.Endpoint, m.Mirror.PathStyle)
	}
}
