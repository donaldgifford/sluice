// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

import (
	"slices"
	"strings"
	"testing"
)

func TestParsePlatform(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    Platform
		wantErr string
	}{
		{name: "linux amd64", in: "linux_amd64", want: Platform{OS: "linux", Arch: "amd64"}},
		{name: "linux arm64", in: "linux_arm64", want: Platform{OS: "linux", Arch: "arm64"}},
		{name: "darwin amd64", in: "darwin_amd64", want: Platform{OS: "darwin", Arch: "amd64"}},
		{name: "darwin arm64", in: "darwin_arm64", want: Platform{OS: "darwin", Arch: "arm64"}},
		{name: "windows amd64", in: "windows_amd64", want: Platform{OS: "windows", Arch: "amd64"}},
		{
			name:    "outside the matrix",
			in:      "linux_riscv64",
			wantErr: `unknown platform "linux_riscv64"`,
		},
		{
			name:    "empty string",
			in:      "",
			wantErr: `unknown platform ""`,
		},
		{
			name:    "case sensitive",
			in:      "Linux_amd64",
			wantErr: `unknown platform "Linux_amd64"`,
		},
		{
			name:    "missing separator",
			in:      "linuxamd64",
			wantErr: `unknown platform "linuxamd64"`,
		},
		{
			name:    "os arch swapped",
			in:      "amd64_linux",
			wantErr: `unknown platform "amd64_linux"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePlatform(tt.in)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParsePlatform(%q) = %v, want error containing %q", tt.in, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParsePlatform(%q) error = %q, want it to contain %q", tt.in, err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), "known platforms:") {
					t.Fatalf("ParsePlatform(%q) error = %q, want it to name the known set", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePlatform(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParsePlatform(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestPlatformStringRoundTrip(t *testing.T) {
	t.Parallel()

	for _, p := range KnownPlatforms() {
		got, err := ParsePlatform(p.String())
		if err != nil {
			t.Fatalf("ParsePlatform(%q) unexpected error: %v", p.String(), err)
		}
		if got != p {
			t.Fatalf("round trip %q = %v, want %v", p.String(), got, p)
		}
	}
}

func TestKnownPlatforms(t *testing.T) {
	t.Parallel()

	ps := KnownPlatforms()
	if len(ps) != len(knownPlatforms) {
		t.Fatalf("KnownPlatforms() returned %d entries, matrix has %d", len(ps), len(knownPlatforms))
	}

	sorted := slices.IsSortedFunc(ps, func(a, b Platform) int {
		return strings.Compare(a.String(), b.String())
	})
	if !sorted {
		t.Fatalf("KnownPlatforms() not sorted by os_arch name: %v", ps)
	}

	// The returned slice is a copy: mutating it must not leak into the
	// matrix.
	ps[0] = Platform{OS: "plan9", Arch: "mips"}
	if fresh := KnownPlatforms(); fresh[0] == ps[0] {
		t.Fatal("KnownPlatforms() shares backing storage with the matrix")
	}
}
