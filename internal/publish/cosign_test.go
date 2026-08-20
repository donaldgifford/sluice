// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/registry"
)

// fakeRunner records invocations and simulates cosign: answers
// version queries and creates the output files sign-blob/attest-blob
// are asked for.
type fakeRunner struct {
	calls   [][]string
	version string // gitVersion answered to "version --json"
	failOn  string // subcommand that should fail ("" = none)
}

func (r *fakeRunner) run(_ context.Context, bin string, args []string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{bin}, args...))
	if len(args) == 0 {
		return nil, errors.New("no args")
	}
	sub := args[0]
	if r.failOn == sub {
		return nil, errors.New("induced " + sub + " failure")
	}
	if sub == "version" {
		return []byte(`{"gitVersion":"` + r.version + `"}`), nil
	}
	// Create every --output-* file named in the args.
	for i, a := range args {
		if strings.HasPrefix(a, "--output-") && i+1 < len(args) {
			if err := os.WriteFile(args[i+1], []byte(sub+" output"), 0o600); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func newTestSigner(t *testing.T, keyRef, version string) (*Signer, *fakeRunner) {
	t.Helper()

	r := &fakeRunner{version: version}
	s := NewSigner(keyRef, "commit-sha-123")
	s.run = r.run
	s.lookPath = func(string) (string, error) { return "/fake/cosign", nil }
	return s, r
}

func testArtifact(t *testing.T) *registry.Artifact {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "terraform-provider-null_3.2.4_linux_amd64.zip")
	if err := os.WriteFile(path, []byte("verified zip"), 0o600); err != nil {
		t.Fatalf("writing artifact: %v", err)
	}
	return &registry.Artifact{
		Source:       "registry.terraform.io/hashicorp/null",
		Version:      "3.2.4",
		Platform:     config.Platform{OS: "linux", Arch: "amd64"},
		Path:         path,
		Filename:     filepath.Base(path),
		SHA256:       "abc123",
		H1:           "h1:fake",
		SigningKeyID: "34365D9472D7468F",
	}
}

func TestSignerPreflight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		wantMsg string
	}{
		{name: "pinned version passes", version: "v2.6.1"},
		{name: "floor minor passes", version: "v2.4.0"},
		{name: "older minor fails", version: "v2.3.9", wantMsg: "unsupported"},
		{name: "different major fails", version: "v3.0.0", wantMsg: "unsupported"},
		{name: "v1 fails", version: "v1.13.7", wantMsg: "unsupported"},
		{name: "garbage version fails", version: "not-semver", wantMsg: "parsing cosign version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, _ := newTestSigner(t, "", tt.version)
			err := s.Preflight(context.Background())
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("Preflight() unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("Preflight() error = %v, want containing %q", err, tt.wantMsg)
			}
		})
	}
}

func TestSignerPreflightMissingBinary(t *testing.T) {
	t.Parallel()

	s := NewSigner("", "")
	s.lookPath = func(string) (string, error) { return "", errors.New("not in PATH") }
	err := s.Preflight(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cosign not found") {
		t.Fatalf("Preflight() error = %v, want missing-binary failure", err)
	}
}

func TestSignAndAttest(t *testing.T) {
	t.Parallel()

	t.Run("keyless produces sig and attestation with predicate", func(t *testing.T) {
		t.Parallel()

		s, r := newTestSigner(t, "", "v2.6.1")
		if err := s.Preflight(context.Background()); err != nil {
			t.Fatalf("Preflight() error: %v", err)
		}
		art := testArtifact(t)
		if err := s.SignAndAttest(context.Background(), art); err != nil {
			t.Fatalf("SignAndAttest() unexpected error: %v", err)
		}

		for _, suffix := range []string{".sig", ".pem", ".intoto.jsonl"} {
			if _, err := os.Stat(art.Path + suffix); err != nil {
				t.Errorf("missing output %s: %v", suffix, err)
			}
		}

		// Predicate carries the artifact's crypto facts verbatim.
		pred, err := os.ReadFile(art.Path + ".predicate.json")
		if err != nil {
			t.Fatalf("reading predicate: %v", err)
		}
		var p map[string]string
		if err := json.Unmarshal(pred, &p); err != nil {
			t.Fatalf("parsing predicate: %v", err)
		}
		want := map[string]string{
			"provider":                art.Source,
			"version":                 "3.2.4",
			"platform":                "linux_amd64",
			"sha256":                  "abc123",
			"h1":                      "h1:fake",
			"upstream_signing_key_id": "34365D9472D7468F",
			"authorizing_commit":      "commit-sha-123",
		}
		for k, v := range want {
			if p[k] != v {
				t.Errorf("predicate[%s] = %q, want %q", k, p[k], v)
			}
		}

		// Invocation shape: version, sign-blob, attest-blob; keyless
		// means no --key anywhere; --yes everywhere.
		if len(r.calls) != 3 {
			t.Fatalf("calls = %d, want 3", len(r.calls))
		}
		signCall, attestCall := r.calls[1], r.calls[2]
		if signCall[1] != "sign-blob" || attestCall[1] != "attest-blob" {
			t.Errorf("call order = %v then %v, want sign-blob then attest-blob", signCall[1], attestCall[1])
		}
		for _, call := range [][]string{signCall, attestCall} {
			if slices.Contains(call, "--key") {
				t.Errorf("keyless call carries --key: %v", call)
			}
			if !slices.Contains(call, "--yes") {
				t.Errorf("call missing --yes: %v", call)
			}
		}
		if !slices.Contains(attestCall, predicateType) {
			t.Errorf("attest call missing predicate type: %v", attestCall)
		}
	})

	t.Run("key-based signing passes --key", func(t *testing.T) {
		t.Parallel()

		s, r := newTestSigner(t, "awskms://alias/sluice", "v2.6.1")
		if err := s.Preflight(context.Background()); err != nil {
			t.Fatalf("Preflight() error: %v", err)
		}
		if err := s.SignAndAttest(context.Background(), testArtifact(t)); err != nil {
			t.Fatalf("SignAndAttest() unexpected error: %v", err)
		}
		for _, call := range r.calls[1:] {
			idx := slices.Index(call, "--key")
			if idx < 0 || call[idx+1] != "awskms://alias/sluice" {
				t.Errorf("call missing --key ref: %v", call)
			}
		}
	})

	t.Run("sign failure aborts before attest", func(t *testing.T) {
		t.Parallel()

		s, r := newTestSigner(t, "", "v2.6.1")
		if err := s.Preflight(context.Background()); err != nil {
			t.Fatalf("Preflight() error: %v", err)
		}
		r.failOn = "sign-blob"
		err := s.SignAndAttest(context.Background(), testArtifact(t))
		if err == nil || !strings.Contains(err.Error(), "signing") {
			t.Fatalf("SignAndAttest() error = %v, want signing failure", err)
		}
		if len(r.calls) != 2 { // version + sign-blob only
			t.Errorf("calls = %d, want no attest after failed sign", len(r.calls))
		}
	})

	t.Run("use before preflight fails", func(t *testing.T) {
		t.Parallel()

		s := NewSigner("", "")
		err := s.SignAndAttest(context.Background(), testArtifact(t))
		if err == nil || !strings.Contains(err.Error(), "before Preflight") {
			t.Fatalf("SignAndAttest() error = %v, want preflight guard", err)
		}
	})
}
