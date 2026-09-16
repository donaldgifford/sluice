// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/publish"
	"github.com/donaldgifford/sluice/internal/registry"
)

// fakeFetcher stages a canned zip; the cmd layer only wires, so the
// crypto facts are nominal.
type fakeFetcher struct {
	calls int
}

func (f *fakeFetcher) FetchVerified(_ context.Context, source, version string, p config.Platform, destDir string) (*registry.Artifact, error) {
	f.calls++
	typ := source[strings.LastIndex(source, "/")+1:]
	name := fmt.Sprintf("terraform-provider-%s_%s_%s.zip", typ, version, p)
	path := filepath.Join(destDir, name)
	if err := os.WriteFile(path, []byte("zip"), 0o600); err != nil {
		return nil, err
	}
	return &registry.Artifact{
		Source: source, Version: version, Platform: p,
		Path: path, Filename: name,
		SHA256: "sha", H1: "h1:fake", SigningKeyID: "KEY",
	}, nil
}

// installFakeCosign puts a working cosign stand-in first on PATH. It
// answers version --json and creates every --output-* file.
func installFakeCosign(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "version" ]; then
  echo '{"gitVersion":"v2.6.1"}'
  exit 0
fi
prev=""
for a in "$@"; do
  case "$prev" in
    --output-signature|--output-certificate|--output-attestation) echo fake > "$a" ;;
  esac
  prev="$a"
done
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "cosign"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake cosign: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func testApplyDeps(b *fakeBucket) (applyDeps, *fakeFetcher) {
	f := &fakeFetcher{}
	return applyDeps{
		bucket: b,
		fetch:  f,
		signer: publish.NewSigner("", "test-commit"),
		log:    slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
	}, f
}

func TestRunApplyJSONRequiresAutoApprove(t *testing.T) {
	t.Parallel()

	deps, _ := testApplyDeps(&fakeBucket{})
	err := runApply(context.Background(), loadPlanManifest(t), deps,
		applyOpts{jsonOut: true}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "requires --auto-approve") {
		t.Fatalf("runApply() error = %v, want auto-approve requirement", err)
	}
}

func TestRunApplyEmptyPlanIsNoOp(t *testing.T) {
	t.Parallel()

	b := syncedBucket()
	deps, ff := testApplyDeps(b)
	var out bytes.Buffer
	err := runApply(context.Background(), loadPlanManifest(t), deps,
		applyOpts{}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("runApply() unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "No changes.") {
		t.Errorf("output = %q", out.String())
	}
	if ff.calls != 0 || len(b.ops) != 0 {
		t.Error("empty plan must touch nothing")
	}
}

func TestRunApplyDeclinedConfirmationChangesNothing(t *testing.T) {
	t.Parallel()

	b := driftedBucket()
	b.writable = true
	deps, ff := testApplyDeps(b)
	var out bytes.Buffer

	err := runApply(context.Background(), loadPlanManifest(t), deps,
		applyOpts{}, strings.NewReader("no\n"), &out)
	if err == nil || !strings.Contains(err.Error(), "apply cancelled") {
		t.Fatalf("runApply() error = %v, want cancellation", err)
	}
	if ff.calls != 0 || len(b.ops) != 0 {
		t.Error("declined apply must touch nothing")
	}
	// The plan and the prompt were shown before the decline.
	if !strings.Contains(out.String(), "Plan: 1 to add") || !strings.Contains(out.String(), "Only 'yes' is accepted") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunApplyEOFDeclines(t *testing.T) {
	t.Parallel()

	b := driftedBucket()
	b.writable = true
	deps, _ := testApplyDeps(b)
	err := runApply(context.Background(), loadPlanManifest(t), deps,
		applyOpts{}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "apply cancelled") {
		t.Fatalf("runApply() error = %v, want cancellation on EOF", err)
	}
	if len(b.ops) != 0 {
		t.Error("EOF-declined apply must touch nothing")
	}
}

// TestRunApplyConfirmedEndToEnd drives the full wiring: confirmation,
// fetch, fake cosign on PATH, ordered publish, convergence.
func TestRunApplyConfirmedEndToEnd(t *testing.T) {
	installFakeCosign(t) // t.Setenv: no t.Parallel

	b := driftedBucket()
	b.writable = true
	deps, ff := testApplyDeps(b)
	m := loadPlanManifest(t)
	var out bytes.Buffer

	err := runApply(context.Background(), m, deps, applyOpts{}, strings.NewReader("yes\n"), &out)
	if err != nil {
		t.Fatalf("runApply() unexpected error: %v", err)
	}
	// add 6.3.0 (2 platforms) + add_platform darwin_arm64 = 3 fetches.
	if ff.calls != 3 {
		t.Errorf("fetch calls = %d, want 3", ff.calls)
	}
	// Converged: a follow-up plan is empty.
	var replan bytes.Buffer
	if err := runPlan(context.Background(), m, b, planOpts{}, &replan); err != nil {
		t.Fatalf("post-apply plan error: %v", err)
	}
	if !strings.Contains(replan.String(), "No changes.") {
		t.Errorf("post-apply plan = %q, want converged", replan.String())
	}
	// Removal semantics at the cmd level: 6.1.0 gone from the index,
	// its doc deleted, its zip untouched (it was never in the fake,
	// but the delete op list shows only the doc). Probe scratch keys
	// are not mirror state.
	for _, op := range nonProbeOps(b) {
		if strings.HasPrefix(op, "delete ") && !strings.Contains(op, "6.1.0.json") {
			t.Errorf("unexpected delete: %s", op)
		}
	}
}

func TestRunApplyAutoApproveSkipsPrompt(t *testing.T) {
	installFakeCosign(t) // t.Setenv: no t.Parallel

	b := driftedBucket()
	b.writable = true
	deps, _ := testApplyDeps(b)
	var out bytes.Buffer

	// Empty stdin: would decline if prompted; auto-approve must not
	// prompt.
	err := runApply(context.Background(), loadPlanManifest(t), deps,
		applyOpts{autoApprove: true}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("runApply() unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "Only 'yes' is accepted") {
		t.Error("auto-approve must not prompt")
	}
}

// degradedBucket returns a writable Garage-shaped fake: preconditions
// silently ignored, no version IDs.
func degradedBucket() *fakeBucket {
	b := driftedBucket()
	b.writable = true
	b.degraded = true
	return b
}

// nonProbeOps filters probe scratch keys: the gate tests assert on
// mirror-state writes only.
func nonProbeOps(b *fakeBucket) []string {
	var out []string
	for _, op := range b.ops {
		key := strings.TrimPrefix(strings.TrimPrefix(op, "put "), "delete ")
		if strings.HasPrefix(key, "_sluice/") {
			continue
		}
		out = append(out, op)
	}
	return out
}

func TestRunApplyDegradedRefusesWithoutFlag(t *testing.T) {
	b := degradedBucket()
	deps, ff := testApplyDeps(b)
	var out bytes.Buffer

	err := runApply(context.Background(), loadPlanManifest(t), deps,
		applyOpts{autoApprove: true}, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "--allow-unversioned-backend") {
		t.Fatalf("runApply() error = %v, want degraded refusal naming the flag", err)
	}
	// Refusal precedes fetch and every mirror-state write.
	if ff.calls != 0 {
		t.Errorf("fetch calls = %d, want none", ff.calls)
	}
	if ops := nonProbeOps(b); len(ops) != 0 {
		t.Errorf("mirror ops = %v, want none", ops)
	}
}

func TestRunApplyDegradedOptInProceeds(t *testing.T) {
	installFakeCosign(t) // t.Setenv: no t.Parallel

	b := degradedBucket()
	var logOut bytes.Buffer
	f := &fakeFetcher{}
	deps := applyDeps{
		bucket: b,
		fetch:  f,
		signer: publish.NewSigner("", "test-commit"),
		log:    slog.New(slog.NewJSONHandler(&logOut, nil)),
	}
	var out bytes.Buffer

	err := runApply(context.Background(), loadPlanManifest(t), deps,
		applyOpts{autoApprove: true, allowUnversioned: true}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("runApply() unexpected error: %v", err)
	}
	if f.calls == 0 {
		t.Error("opted-in degraded apply fetched nothing")
	}
	if !strings.Contains(logOut.String(), "degraded backend") {
		t.Errorf("no degraded WARN in audit log: %s", logOut.String())
	}
	// Yanked 6.1.0 retired (degraded mode keeps forensics under _retired/).
	found := false
	for _, op := range b.ops {
		if op == "put _retired/"+awsSrc+"/6.1.0.json" {
			found = true
		}
	}
	if !found {
		t.Errorf("ops = %v, want retired 6.1.0 doc", b.ops)
	}
}
