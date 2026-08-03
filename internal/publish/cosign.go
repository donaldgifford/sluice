// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/donaldgifford/sluice/internal/registry"
)

const (
	// predicateType identifies sluice's ingest attestation.
	predicateType = "https://github.com/donaldgifford/sluice/provider-ingest/v1"

	// Cosign version floor. Tied to the mise.toml pin — bump
	// together. Major must be 2: cosign 2.x is the current CLI
	// contract; anything older (or a hypothetical 3.x with changed
	// flags) fails closed.
	requiredCosignMajor = 2
	minCosignMinor      = 4
)

// runner executes a binary and returns its combined stdout. The
// production implementation shells out; tests inject a recorder.
type runner func(ctx context.Context, bin string, args []string) ([]byte, error)

func execRunner(ctx context.Context, bin string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // G204: bin from LookPath("cosign"), args program-built
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, exitErr.Stderr)
		}
		return nil, fmt.Errorf("%s %s: %w", bin, strings.Join(args, " "), err)
	}
	return out, nil
}

// Signer shells out to the mise-pinned cosign binary for per-artifact
// signatures and in-toto attestations. Zero value is not usable;
// construct with [NewSigner] and call [Signer.Preflight] before any
// mirror work — a missing or wrong-version cosign must abort the
// apply before anything is fetched or written.
type Signer struct {
	run      runner
	lookPath func(string) (string, error)
	bin      string
	keyRef   string // --key (KMS/file); "" = keyless OIDC
	commit   string // authorizing commit recorded in the predicate
}

// NewSigner returns a Signer. keyRef selects --key signing (KMS or
// file); empty means keyless OIDC. commit is provenance metadata for
// the attestation predicate; empty is permitted outside CI.
func NewSigner(keyRef, commit string) *Signer {
	return &Signer{
		run:      execRunner,
		lookPath: exec.LookPath,
		keyRef:   keyRef,
		commit:   commit,
	}
}

// Preflight resolves and version-checks cosign. Fail closed: no
// binary, an unparsable version, or one below the floor is an error.
func (s *Signer) Preflight(ctx context.Context) error {
	bin, err := s.lookPath("cosign")
	if err != nil {
		return fmt.Errorf("cosign not found — install the mise-pinned toolchain (mise install): %w", err)
	}
	out, err := s.run(ctx, bin, []string{"version", "--json"})
	if err != nil {
		return fmt.Errorf("checking cosign version: %w", err)
	}
	var v struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return fmt.Errorf("parsing cosign version output: %w", err)
	}
	major, minor, err := parseMajorMinor(v.GitVersion)
	if err != nil {
		return fmt.Errorf("parsing cosign version %q: %w", v.GitVersion, err)
	}
	if major != requiredCosignMajor || minor < minCosignMinor {
		return fmt.Errorf("cosign %s is unsupported: need %d.%d or newer (same major)",
			v.GitVersion, requiredCosignMajor, minCosignMinor)
	}
	s.bin = bin
	return nil
}

// SignAndAttest signs the staged artifact and writes its in-toto
// attestation, placing outputs next to the zip: <zip>.sig,
// <zip>.pem, <zip>.intoto.jsonl. Called before upload so an unsigned
// artifact can never reach the bucket.
func (s *Signer) SignAndAttest(ctx context.Context, art *registry.Artifact) error {
	if s.bin == "" {
		return errors.New("signer used before Preflight")
	}

	predPath := art.Path + ".predicate.json"
	pred, err := json.Marshal(map[string]string{
		"provider":                art.Source,
		"version":                 art.Version,
		"platform":                art.Platform.String(),
		"sha256":                  art.SHA256,
		"h1":                      art.H1,
		"upstream_signing_key_id": art.SigningKeyID,
		"authorizing_commit":      s.commit,
	})
	if err != nil {
		return fmt.Errorf("encoding attestation predicate: %w", err)
	}
	if err := os.WriteFile(predPath, pred, 0o600); err != nil {
		return fmt.Errorf("writing attestation predicate: %w", err)
	}

	signArgs := []string{
		"sign-blob", "--yes",
		"--output-signature", art.Path + ".sig",
		"--output-certificate", art.Path + ".pem",
	}
	attestArgs := []string{
		"attest-blob", "--yes",
		"--predicate", predPath,
		"--type", predicateType,
		"--output-attestation", art.Path + ".intoto.jsonl",
		"--output-certificate", art.Path + ".att.pem",
	}
	if s.keyRef != "" {
		signArgs = append(signArgs, "--key", s.keyRef)
		attestArgs = append(attestArgs, "--key", s.keyRef)
	}
	signArgs = append(signArgs, art.Path)
	attestArgs = append(attestArgs, art.Path)

	if _, err := s.run(ctx, s.bin, signArgs); err != nil {
		return fmt.Errorf("signing %s: %w", art.Filename, err)
	}
	if _, err := s.run(ctx, s.bin, attestArgs); err != nil {
		return fmt.Errorf("attesting %s: %w", art.Filename, err)
	}
	return nil
}

func parseMajorMinor(gitVersion string) (major, minor int, err error) {
	parts := strings.Split(strings.TrimPrefix(gitVersion, "v"), ".")
	if len(parts) < 2 {
		return 0, 0, errors.New("want at least major.minor")
	}
	if major, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, fmt.Errorf("major %q: %w", parts[0], err)
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, fmt.Errorf("minor %q: %w", parts[1], err)
	}
	return major, minor, nil
}
