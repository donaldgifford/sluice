// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/publish"
)

// fakeBucket is the minimal read-side fake plan needs; Put/Delete
// exist only to satisfy the interface.
type fakeBucket struct {
	objects map[string][]byte
}

func (f *fakeBucket) Get(_ context.Context, key string) ([]byte, string, error) {
	body, ok := f.objects[key]
	if !ok {
		return nil, "", fmt.Errorf("fake get %s: %w", key, publish.ErrNotFound)
	}
	return body, "etag-" + key, nil
}

func (*fakeBucket) Put(context.Context, string, string, []byte, publish.Cond) (string, error) {
	return "", errors.New("plan must never write")
}

func (*fakeBucket) Delete(context.Context, string) (string, error) {
	return "", errors.New("plan must never delete")
}

func (f *fakeBucket) List(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

var _ publish.Bucket = (*fakeBucket)(nil)

const (
	awsSrc = "registry.terraform.io/hashicorp/aws"
	cfSrc  = "registry.terraform.io/cloudflare/cloudflare"
)

// driftedBucket seeds actual state that diverges from testdata/plan.hcl
// in all three ways: 6.3.0 missing (add), 6.1.0 present (remove),
// cloudflare 5.4.0 missing darwin_arm64 (add_platform).
func driftedBucket() *fakeBucket {
	doc := func(plats ...string) []byte {
		out := `{"archives":{`
		for i, p := range plats {
			if i > 0 {
				out += ","
			}
			out += `"` + p + `":{"url":"x_` + p + `.zip","hashes":["h1:x"]}`
		}
		return []byte(out + `}}`)
	}
	return &fakeBucket{objects: map[string][]byte{
		awsSrc + "/index.json": []byte(`{"versions":{"6.1.0":{},"6.2.0":{}}}`),
		awsSrc + "/6.1.0.json": doc("darwin_arm64", "linux_amd64"),
		awsSrc + "/6.2.0.json": doc("darwin_arm64", "linux_amd64"),
		cfSrc + "/index.json":  []byte(`{"versions":{"5.4.0":{}}}`),
		cfSrc + "/5.4.0.json":  doc("linux_amd64"),
	}}
}

// syncedBucket seeds actual state exactly matching testdata/plan.hcl.
func syncedBucket() *fakeBucket {
	b := driftedBucket()
	b.objects[awsSrc+"/index.json"] = []byte(`{"versions":{"6.2.0":{},"6.3.0":{}}}`)
	b.objects[awsSrc+"/6.3.0.json"] = b.objects[awsSrc+"/6.2.0.json"]
	delete(b.objects, awsSrc+"/6.1.0.json")
	b.objects[awsSrc+"/index.json"] = []byte(`{"versions":{"6.2.0":{},"6.3.0":{}}}`)
	b.objects[cfSrc+"/5.4.0.json"] = []byte(
		`{"archives":{"darwin_arm64":{"url":"x.zip","hashes":["h1:x"]},"linux_amd64":{"url":"y.zip","hashes":["h1:y"]}}}`)
	return b
}

func loadPlanManifest(t *testing.T) *config.Manifest {
	t.Helper()

	m, err := config.LoadFile("testdata/plan.hcl")
	if err != nil {
		t.Fatalf("loading plan manifest: %v", err)
	}
	return m
}

func TestRunPlanHuman(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runPlan(context.Background(), loadPlanManifest(t), driftedBucket(), planOpts{}, &out)
	if err != nil {
		t.Fatalf("runPlan() unexpected error: %v", err)
	}
	want := `registry.terraform.io/cloudflare/cloudflare
  ~ 5.4.0  +darwin_arm64  (platform add)

registry.terraform.io/hashicorp/aws
  + 6.3.0  [darwin_arm64, linux_amd64]
  - 6.1.0  [darwin_arm64, linux_amd64]  (yank)

Plan: 1 to add, 1 to remove, 1 platform change.
`
	if out.String() != want {
		t.Errorf("runPlan() output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestRunPlanHumanEmpty(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runPlan(context.Background(), loadPlanManifest(t), syncedBucket(), planOpts{detailed: true}, &out)
	if err != nil {
		t.Fatalf("runPlan() unexpected error: %v", err)
	}
	if got := out.String(); got != "No changes. Mirror matches the manifest.\n" {
		t.Errorf("runPlan() output = %q", got)
	}
}

// TestRunPlanJSONGolden freezes the --json bytes — the comment-bot
// contract. Any diff here is a breaking change to a published
// interface.
func TestRunPlanJSONGolden(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runPlan(context.Background(), loadPlanManifest(t), driftedBucket(), planOpts{jsonOut: true}, &out)
	if err != nil {
		t.Fatalf("runPlan() unexpected error: %v", err)
	}
	golden := filepath.Join("testdata", "plan.golden.json")
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("plan --json drifted from %s:\ngot:\n%s\nwant:\n%s", golden, out.Bytes(), want)
	}
}

func TestRunPlanDetailedExitcode(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runPlan(context.Background(), loadPlanManifest(t), driftedBucket(), planOpts{detailed: true}, &out)
	if !errors.Is(err, errChangesPresent) {
		t.Fatalf("runPlan() error = %v, want errChangesPresent", err)
	}
	// The plan must still have been rendered before the signal.
	if !strings.Contains(out.String(), "Plan: 1 to add") {
		t.Errorf("plan not rendered before exit-2 signal: %q", out.String())
	}
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil is success", nil, 0},
		{"changes present maps to 2", fmt.Errorf("wrap: %w", errChangesPresent), 2},
		{"conflict maps to 3", fmt.Errorf("wrap: %w", publish.ErrConflict), 3},
		{"anything else maps to 1", errors.New("boom"), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := exitCode(tt.err); got != tt.want {
				t.Errorf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
