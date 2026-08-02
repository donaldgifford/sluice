// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execute runs the command tree with args and returns stdout and the
// error from Execute.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

	root := newRootCmd(buildInfo{version: "1.2.3", commit: "abcdef0", date: "2026-08-02"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestValidateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantOut string
		wantErr string
	}{
		{
			name:    "valid file exits clean",
			args:    []string{"validate", "--config-file", "testdata/valid.hcl"},
			wantOut: "Valid: 1 provider(s), 2 default platform(s)",
		},
		{
			name:    "invalid file reports the rule",
			args:    []string{"validate", "--config-file", "testdata/invalid.hcl"},
			wantErr: "version constraints are not supported",
		},
		{
			name:    "missing source flags is a usage error",
			args:    []string{"validate"},
			wantErr: "one of --config-dir or --config-file is required",
		},
		{
			name: "both source flags are mutually exclusive",
			args: []string{
				"validate",
				"--config-dir", "testdata",
				"--config-file", "testdata/valid.hcl",
			},
			wantErr: "none of the others can be",
		},
		{
			name:    "unexpected positional args rejected",
			args:    []string{"validate", "extra"},
			wantErr: "unknown command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := execute(t, tt.args...)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("args %v: expected an error, got none (out: %q)", tt.args, out)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("args %v: error %q does not contain %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("args %v: unexpected error: %v", tt.args, err)
			}
			if !strings.Contains(out, tt.wantOut) {
				t.Fatalf("args %v: output %q does not contain %q", tt.args, out, tt.wantOut)
			}
		})
	}
}

func TestVersionOutput(t *testing.T) {
	t.Parallel()

	out, err := execute(t, "--version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "sluice 1.2.3 (abcdef0, 2026-08-02)\n"
	if out != want {
		t.Fatalf("version output = %q, want %q", out, want)
	}
}

func TestLaterPhaseCommandsAreStubbed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "apply", args: []string{"apply", "--config-file", "testdata/valid.hcl"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := execute(t, tt.args...)
			if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
				t.Fatalf("%s: err = %v, want a not-implemented error", tt.name, err)
			}
		})
	}
}

func TestStubCommandsStillValidateConfig(t *testing.T) {
	t.Parallel()

	// The stubs must run the real load path so flag handling is
	// end-to-end even before their phases land.
	_, err := execute(t, "plan", "--config-file", "testdata/invalid.hcl")
	if err == nil || !strings.Contains(err.Error(), "version constraints are not supported") {
		t.Fatalf("plan on invalid config: err = %v, want the validation error", err)
	}
}

func TestExportCommand(t *testing.T) {
	t.Parallel()

	out, err := execute(t, "export", "--config-file", "testdata/valid.hcl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{
  "providers": {
    "registry.terraform.io/hashicorp/aws": [
      "6.3.0"
    ]
  }
}
`
	if out != want {
		t.Fatalf("export output = %q, want %q", out, want)
	}
}

func TestExportCommandOutFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "providers.json")
	stdout, err := execute(t, "export", "--config-file", "testdata/valid.hcl", "--out", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty when --out is set", stdout)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading --out file: %v", err)
	}
	if !strings.Contains(string(data), `"registry.terraform.io/hashicorp/aws"`) {
		t.Fatalf("--out content = %q, missing the provider key", data)
	}
}

func TestBootstrapValidateRoundTrip(t *testing.T) {
	t.Parallel()

	// The Phase 2 success criterion, end to end at the command layer:
	// bootstrap output piped into validate exits clean.
	manifest := filepath.Join(t.TempDir(), "manifest.hcl")
	if _, err := execute(t,
		"bootstrap", "../../internal/bootstrap/testdata/tree", "--out", manifest,
	); err != nil {
		t.Fatalf("bootstrap: unexpected error: %v", err)
	}

	out, err := execute(t, "validate", "--config-file", manifest)
	if err != nil {
		t.Fatalf("validate on bootstrap output: %v", err)
	}
	if !strings.Contains(out, "Valid: 3 provider(s)") {
		t.Fatalf("validate output = %q, want the 3 seeded providers", out)
	}
}
