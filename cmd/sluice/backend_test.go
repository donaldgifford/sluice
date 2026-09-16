// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/sluice/internal/config"
)

// backendCmd builds a throwaway command carrying the backend flags.
// t.Setenv forbids t.Parallel below.
func backendCmd(t *testing.T) (*cobra.Command, *backendFlags) {
	t.Helper()

	cmd := &cobra.Command{Use: "test"}
	return cmd, addBackendFlags(cmd)
}

func backendManifest(endpoint string, pathStyle bool) *config.Manifest {
	return &config.Manifest{
		Mirror: config.Mirror{
			Bucket:    "b",
			Region:    "us-east-1",
			Endpoint:  endpoint,
			PathStyle: pathStyle,
		},
	}
}

func TestBackendResolvePrecedence(t *testing.T) {
	// t.Setenv: no t.Parallel.
	tests := []struct {
		name      string
		hcl       *config.Manifest
		env       map[string]string
		flags     map[string]string
		wantEnd   string
		wantStyle bool
	}{
		{
			name:      "hcl only",
			hcl:       backendManifest("https://hcl.internal", true),
			wantEnd:   "https://hcl.internal",
			wantStyle: true,
		},
		{
			name:      "empty manifest stays AWS",
			hcl:       backendManifest("", false),
			wantEnd:   "",
			wantStyle: false,
		},
		{
			name:      "env beats hcl",
			hcl:       backendManifest("https://hcl.internal", true),
			env:       map[string]string{"SLUICE_S3_ENDPOINT": "https://env.internal", "SLUICE_S3_PATH_STYLE": "true"},
			wantEnd:   "https://env.internal",
			wantStyle: true,
		},
		{
			name:      "flags beat env",
			hcl:       backendManifest("https://hcl.internal", true),
			env:       map[string]string{"SLUICE_S3_ENDPOINT": "https://env.internal", "SLUICE_S3_PATH_STYLE": "true"},
			flags:     map[string]string{"s3-endpoint": "https://flag.internal", "s3-path-style": "true"},
			wantEnd:   "https://flag.internal",
			wantStyle: true,
		},
		{
			name:      "flags beat hcl",
			hcl:       backendManifest("", false),
			flags:     map[string]string{"s3-endpoint": "https://flag.internal", "s3-path-style": "true"},
			wantEnd:   "https://flag.internal",
			wantStyle: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv: no t.Parallel.
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			cmd, bf := backendCmd(t)
			for k, v := range tt.flags {
				if err := cmd.Flags().Set(k, v); err != nil {
					t.Fatalf("setting flag %s: %v", k, err)
				}
			}
			endpoint, pathStyle, err := bf.resolve(tt.hcl)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if endpoint != tt.wantEnd || pathStyle != tt.wantStyle {
				t.Fatalf("got (%q, %v), want (%q, %v)",
					endpoint, pathStyle, tt.wantEnd, tt.wantStyle)
			}
		})
	}
}

func TestBackendResolveErrors(t *testing.T) {
	// t.Setenv: no t.Parallel.
	tests := []struct {
		name  string
		hcl   *config.Manifest
		env   map[string]string
		flags map[string]string
		want  string
	}{
		{
			name: "endpoint without path style from hcl",
			hcl:  backendManifest("https://hcl.internal", false),
			want: "path_style must be true when endpoint is set",
		},
		{
			name: "env endpoint without path style",
			hcl:  backendManifest("", false),
			env:  map[string]string{"SLUICE_S3_ENDPOINT": "https://env.internal"},
			want: "path_style must be true when endpoint is set",
		},
		{
			name: "env endpoint with explicit false path style",
			hcl:  backendManifest("", false),
			env:  map[string]string{"SLUICE_S3_ENDPOINT": "https://env.internal", "SLUICE_S3_PATH_STYLE": "false"},
			want: "path_style must be true when endpoint is set",
		},
		{
			name:  "flag endpoint without path style",
			hcl:   backendManifest("", false),
			flags: map[string]string{"s3-endpoint": "https://flag.internal"},
			want:  "path_style must be true when endpoint is set",
		},
		{
			name: "garbage path style env",
			hcl:  backendManifest("", false),
			env:  map[string]string{"SLUICE_S3_PATH_STYLE": "maybe"},
			want: `invalid SLUICE_S3_PATH_STYLE "maybe"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv: no t.Parallel.
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			cmd, bf := backendCmd(t)
			for k, v := range tt.flags {
				if err := cmd.Flags().Set(k, v); err != nil {
					t.Fatalf("setting flag %s: %v", k, err)
				}
			}
			if _, _, err := bf.resolve(tt.hcl); err == nil ||
				!strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestBackendApplyWritesManifest(t *testing.T) {
	// t.Setenv: no t.Parallel.
	t.Setenv("SLUICE_S3_ENDPOINT", "https://env.internal")
	t.Setenv("SLUICE_S3_PATH_STYLE", "true")

	_, bf := backendCmd(t)
	m := backendManifest("", false)
	if err := bf.apply(m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Mirror.Endpoint != "https://env.internal" || !m.Mirror.PathStyle {
		t.Fatalf("manifest = (%q, %v), want env values",
			m.Mirror.Endpoint, m.Mirror.PathStyle)
	}
}
