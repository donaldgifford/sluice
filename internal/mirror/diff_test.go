// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package mirror

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ps builds a ProviderState from version → platforms.
func ps(versions map[string][]string) ProviderState {
	out := make(ProviderState, len(versions))
	for v, platforms := range versions {
		set := make(PlatformSet, len(platforms))
		for _, p := range platforms {
			set[p] = struct{}{}
		}
		out[v] = set
	}
	return out
}

func TestDiff(t *testing.T) {
	t.Parallel()

	const (
		aws = "registry.terraform.io/hashicorp/aws"
		cf  = "registry.terraform.io/cloudflare/cloudflare"
	)
	both := []string{"darwin_arm64", "linux_amd64"}

	tests := []struct {
		name    string
		desired State
		actual  State
		want    Plan
	}{
		{
			name:    "empty desired and actual is an empty plan",
			desired: State{},
			actual:  State{},
			want:    Plan{Add: []VersionChange{}, Remove: []VersionRef{}, AddPlatform: []VersionChange{}},
		},
		{
			name:    "identical states produce an empty plan",
			desired: State{aws: ps(map[string][]string{"6.3.0": both})},
			actual:  State{aws: ps(map[string][]string{"6.3.0": both})},
			want:    Plan{Add: []VersionChange{}, Remove: []VersionRef{}, AddPlatform: []VersionChange{}},
		},
		{
			name:    "new provider adds every version with full platforms",
			desired: State{aws: ps(map[string][]string{"6.2.0": both, "6.3.0": both})},
			actual:  State{},
			want: Plan{
				Add: []VersionChange{
					{Provider: aws, Version: "6.2.0", Platforms: both},
					{Provider: aws, Version: "6.3.0", Platforms: both},
				},
				Remove:      []VersionRef{},
				AddPlatform: []VersionChange{},
			},
		},
		{
			name: "new version on a mirrored provider",
			desired: State{aws: ps(map[string][]string{
				"6.2.0": both,
				"6.3.0": both,
			})},
			actual: State{aws: ps(map[string][]string{"6.2.0": both})},
			want: Plan{
				Add:         []VersionChange{{Provider: aws, Version: "6.3.0", Platforms: both}},
				Remove:      []VersionRef{},
				AddPlatform: []VersionChange{},
			},
		},
		{
			name:    "removed version",
			desired: State{aws: ps(map[string][]string{"6.3.0": both})},
			actual:  State{aws: ps(map[string][]string{"6.2.0": both, "6.3.0": both})},
			want: Plan{
				Add:         []VersionChange{},
				Remove:      []VersionRef{{Provider: aws, Version: "6.2.0"}},
				AddPlatform: []VersionChange{},
			},
		},
		{
			name:    "deleted provider block removes every version",
			desired: State{},
			actual:  State{aws: ps(map[string][]string{"6.2.0": both, "6.3.0": both})},
			want: Plan{
				Add: []VersionChange{},
				Remove: []VersionRef{
					{Provider: aws, Version: "6.2.0"},
					{Provider: aws, Version: "6.3.0"},
				},
				AddPlatform: []VersionChange{},
			},
		},
		{
			name:    "platform gap adds only the missing platforms",
			desired: State{cf: ps(map[string][]string{"5.4.0": both})},
			actual:  State{cf: ps(map[string][]string{"5.4.0": {"linux_amd64"}})},
			want: Plan{
				Add:         []VersionChange{},
				Remove:      []VersionRef{},
				AddPlatform: []VersionChange{{Provider: cf, Version: "5.4.0", Platforms: []string{"darwin_arm64"}}},
			},
		},
		{
			name:    "extra actual platform produces no action",
			desired: State{cf: ps(map[string][]string{"5.4.0": {"linux_amd64"}})},
			actual:  State{cf: ps(map[string][]string{"5.4.0": both})},
			want:    Plan{Add: []VersionChange{}, Remove: []VersionRef{}, AddPlatform: []VersionChange{}},
		},
		{
			name: "all three action kinds in one plan",
			desired: State{
				aws: ps(map[string][]string{"6.3.0": both, "6.4.0": both}),
				cf:  ps(map[string][]string{"5.4.0": both}),
			},
			actual: State{
				aws: ps(map[string][]string{"6.1.0": both, "6.3.0": both}),
				cf:  ps(map[string][]string{"5.4.0": {"linux_amd64"}}),
			},
			want: Plan{
				Add:         []VersionChange{{Provider: aws, Version: "6.4.0", Platforms: both}},
				Remove:      []VersionRef{{Provider: aws, Version: "6.1.0"}},
				AddPlatform: []VersionChange{{Provider: cf, Version: "5.4.0", Platforms: []string{"darwin_arm64"}}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Diff(tt.desired, tt.actual)
			if err != nil {
				t.Fatalf("Diff() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Fatalf("Diff() =\n%+v\nwant\n%+v", *got, tt.want)
			}
			if wantEmpty := len(tt.want.Add)+len(tt.want.Remove)+len(tt.want.AddPlatform) == 0; got.Empty() != wantEmpty {
				t.Fatalf("Empty() = %v, want %v", got.Empty(), wantEmpty)
			}
		})
	}
}

func TestDiffOrderingIsDeterministic(t *testing.T) {
	t.Parallel()

	const (
		aws  = "registry.terraform.io/hashicorp/aws"
		null = "registry.opentofu.org/hashicorp/null"
	)

	desired := State{
		aws: ps(map[string][]string{
			// Version-aware: 6.9.0 sorts before 6.10.0, prerelease
			// before its release.
			"6.10.0":      {"linux_amd64"},
			"6.9.0":       {"linux_amd64"},
			"7.0.0-beta1": {"linux_amd64"},
			"7.0.0":       {"linux_amd64"},
		}),
		null: ps(map[string][]string{"3.2.4": {"linux_amd64"}}),
	}

	got, err := Diff(desired, State{})
	if err != nil {
		t.Fatalf("Diff() unexpected error: %v", err)
	}

	order := make([]string, 0, len(got.Add))
	for _, a := range got.Add {
		order = append(order, a.Provider+"@"+a.Version)
	}
	want := []string{
		// opentofu.org sorts before terraform.io lexically.
		null + "@3.2.4",
		aws + "@6.9.0",
		aws + "@6.10.0",
		aws + "@7.0.0-beta1",
		aws + "@7.0.0",
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("Add order = %v, want %v", order, want)
	}
}

func TestDiffCorruptActualVersion(t *testing.T) {
	t.Parallel()

	actual := State{
		"registry.terraform.io/hashicorp/aws": ps(map[string][]string{
			"not-a-version": {"linux_amd64"},
		}),
	}
	_, err := Diff(State{}, actual)
	if err == nil {
		t.Fatal("expected an error for a corrupt actual version key")
	}
	for _, want := range []string{"registry.terraform.io/hashicorp/aws", `"not-a-version"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestPlanMarshalGolden(t *testing.T) {
	t.Parallel()

	plan := &Plan{
		Add: []VersionChange{{
			Provider:  "registry.terraform.io/hashicorp/aws",
			Version:   "6.3.0",
			Platforms: []string{"darwin_arm64", "linux_amd64"},
		}},
		Remove: []VersionRef{{
			Provider: "registry.terraform.io/hashicorp/aws",
			Version:  "6.1.0",
		}},
		AddPlatform: []VersionChange{{
			Provider:  "registry.terraform.io/cloudflare/cloudflare",
			Version:   "5.4.0",
			Platforms: []string{"darwin_arm64"},
		}},
	}

	got, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	golden := filepath.Join("testdata", "golden", "plan.json")
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("plan marshal drifted from %s:\ngot:\n%s\nwant:\n%s", golden, got, want)
	}
}

func TestEmptyPlanMarshalsArraysNotNull(t *testing.T) {
	t.Parallel()

	got, err := Diff(State{}, State{})
	if err != nil {
		t.Fatalf("Diff() unexpected error: %v", err)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"add":[],"remove":[],"add_platform":[]}`
	if string(out) != want {
		t.Fatalf("empty plan = %s, want %s", out, want)
	}
}
