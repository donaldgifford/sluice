// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import "testing"

func TestParseSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		want    providerAddr
		wantErr bool
	}{
		{
			name:   "terraform registry",
			source: "registry.terraform.io/hashicorp/aws",
			want:   providerAddr{"registry.terraform.io", "hashicorp", "aws"},
		},
		{
			name:   "opentofu registry",
			source: "registry.opentofu.org/hashicorp/null",
			want:   providerAddr{"registry.opentofu.org", "hashicorp", "null"},
		},
		{name: "two segments", source: "hashicorp/aws", wantErr: true},
		{name: "four segments", source: "reg.example.com/a/b/c", wantErr: true},
		{name: "empty segment", source: "reg.example.com//aws", wantErr: true},
		{name: "empty", source: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseSource(tt.source)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSource(%q) error = nil, want non-nil", tt.source)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSource(%q) unexpected error: %v", tt.source, err)
			}
			if got != tt.want {
				t.Errorf("parseSource(%q) = %+v, want %+v", tt.source, got, tt.want)
			}
			if got.String() != tt.source {
				t.Errorf("String() = %q, want round-trip to %q", got.String(), tt.source)
			}
		})
	}
}
