// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"testing"

	"github.com/donaldgifford/sluice/internal/config"
)

func TestPolicyFetcherScopesTheOverride(t *testing.T) {
	t.Parallel()

	m := &config.Manifest{Providers: []config.Provider{
		{Source: "registry.terraform.io/hashicorp/null", AllowExpiredSigningKey: true},
		{Source: "registry.opentofu.org/hashicorp/null"},
	}}
	f, err := newPolicyFetcher(m)
	if err != nil {
		t.Fatalf("newPolicyFetcher() unexpected error: %v", err)
	}

	tests := []struct {
		source string
		want   bool
	}{
		{source: "registry.terraform.io/hashicorp/null", want: true},
		{source: "registry.opentofu.org/hashicorp/null", want: false},
		// A provider the manifest never declared can never inherit it.
		{source: "registry.terraform.io/evil/provider", want: false},
	}
	for _, tt := range tests {
		if got := f.allowExpired[tt.source]; got != tt.want {
			t.Errorf("%s: allowExpired = %v, want %v", tt.source, got, tt.want)
		}
	}
}
