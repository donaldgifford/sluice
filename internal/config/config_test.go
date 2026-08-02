// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

import (
	"errors"
	"strings"
	"testing"
)

// header is a valid mirror block for tests that exercise provider
// conversion.
const header = `mirror {
  bucket    = "org-tf-mirror"
  region    = "us-east-1"
  platforms = ["linux_amd64", "darwin_arm64"]
}

`

func TestPlatformResolution(t *testing.T) {
	t.Parallel()

	src := header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0"]
}

provider "registry.terraform.io/cloudflare/cloudflare" {
  versions  = ["5.4.0"]
  platforms = ["darwin_arm64"]
}
`
	m, err := loadBytes("test.hcl", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inherited := m.Providers[0].Platforms
	if len(inherited) != 2 || inherited[0].String() != "linux_amd64" || inherited[1].String() != "darwin_arm64" {
		t.Fatalf("inherited platforms = %v, want the mirror matrix in declared order", inherited)
	}

	overridden := m.Providers[1].Platforms
	if len(overridden) != 1 || overridden[0].String() != "darwin_arm64" {
		t.Fatalf("overridden platforms = %v, want [darwin_arm64]", overridden)
	}
}

func TestUnknownPlatformIssues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "unknown platform in mirror",
			src: `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = ["linux_riscv64"]
}
`,
			want: []string{`mirror: unknown platform "linux_riscv64"`, "known platforms:"},
		},
		{
			name: "unknown platform in provider override",
			src: header + `provider "registry.terraform.io/hashicorp/aws" {
  versions  = ["6.3.0"]
  platforms = ["solaris_sparc"]
}
`,
			want: []string{
				`provider "registry.terraform.io/hashicorp/aws": unknown platform "solaris_sparc"`,
			},
		},
		{
			name: "all violations reported in one pass",
			src: `mirror {
  bucket    = "a"
  region    = "us-east-1"
  platforms = ["linux_riscv64"]
}

provider "registry.terraform.io/hashicorp/aws" {
  versions  = ["6.3.0"]
  platforms = ["solaris_sparc"]
}
`,
			want: []string{`unknown platform "linux_riscv64"`, `unknown platform "solaris_sparc"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadBytes("test.hcl", []byte(tt.src))
			if err == nil {
				t.Fatal("expected an error, got none")
			}

			var valErr *ValidationError
			if !errors.As(err, &valErr) {
				t.Fatalf("error type = %T, want *ValidationError; err: %v", err, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestValidationErrorOneLinePerIssue(t *testing.T) {
	t.Parallel()

	err := &ValidationError{Issues: []Issue{
		{Subject: "mirror", Message: "first"},
		{Subject: `provider "x/y/z"`, Message: "second"},
	}}
	want := "mirror: first\nprovider \"x/y/z\": second"
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestInheritedPlatformsDoNotAliasMirror(t *testing.T) {
	t.Parallel()

	src := header + `provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0"]
}
`
	m, err := loadBytes("test.hcl", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mutating a provider's inherited platforms must not corrupt the
	// mirror matrix (or siblings) through shared backing storage.
	m.Providers[0].Platforms[0] = Platform{OS: "plan9", Arch: "mips"}
	if m.Mirror.Platforms[0] == m.Providers[0].Platforms[0] {
		t.Fatal("Provider.Platforms shares backing storage with Mirror.Platforms")
	}
}
