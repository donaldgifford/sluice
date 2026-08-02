// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"bytes"
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
		{name: "plan", args: []string{"plan", "--config-file", "testdata/valid.hcl"}},
		{name: "apply", args: []string{"apply", "--config-file", "testdata/valid.hcl"}},
		{name: "export", args: []string{"export", "--config-file", "testdata/valid.hcl"}},
		{name: "bootstrap", args: []string{"bootstrap", "."}},
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
