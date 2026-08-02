// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package mirror

import (
	"testing"

	"github.com/donaldgifford/sluice/internal/config"
)

func TestDesired(t *testing.T) {
	t.Parallel()

	linux := config.Platform{OS: "linux", Arch: "amd64"}
	darwin := config.Platform{OS: "darwin", Arch: "arm64"}

	m := &config.Manifest{
		Mirror: config.Mirror{
			Bucket:    "b",
			Region:    "us-east-1",
			Platforms: []config.Platform{linux, darwin},
		},
		Providers: []config.Provider{
			{
				Source:    "registry.terraform.io/hashicorp/aws",
				Versions:  []string{"6.2.0", "6.3.0"},
				Platforms: []config.Platform{linux, darwin}, // inherited
			},
			{
				Source:    "registry.terraform.io/cloudflare/cloudflare",
				Versions:  []string{"5.4.0"},
				Platforms: []config.Platform{linux}, // override
			},
		},
	}

	s := Desired(m)

	if len(s) != 2 {
		t.Fatalf("State has %d providers, want 2", len(s))
	}

	aws := s["registry.terraform.io/hashicorp/aws"]
	if len(aws) != 2 {
		t.Fatalf("aws versions = %v, want 2", aws)
	}
	for _, v := range []string{"6.2.0", "6.3.0"} {
		set, ok := aws[v]
		if !ok {
			t.Fatalf("aws missing version %s", v)
		}
		if len(set) != 2 {
			t.Fatalf("aws %s platforms = %v, want both matrix entries", v, set)
		}
		for _, p := range []string{"linux_amd64", "darwin_arm64"} {
			if _, ok := set[p]; !ok {
				t.Fatalf("aws %s missing platform %s", v, p)
			}
		}
	}

	cf := s["registry.terraform.io/cloudflare/cloudflare"]
	set := cf["5.4.0"]
	if len(set) != 1 {
		t.Fatalf("cloudflare platforms = %v, want the single override", set)
	}
	if _, ok := set["linux_amd64"]; !ok {
		t.Fatalf("cloudflare platforms = %v, want linux_amd64", set)
	}
}

func TestDesiredEmptyManifest(t *testing.T) {
	t.Parallel()

	s := Desired(&config.Manifest{})
	if len(s) != 0 {
		t.Fatalf("State = %v, want empty", s)
	}
}
