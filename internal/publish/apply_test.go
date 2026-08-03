// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/mirror"
	"github.com/donaldgifford/sluice/internal/registry"
)

// fakeFetcher stages a fake zip under the artifact naming convention
// and returns canned crypto facts.
type fakeFetcher struct {
	calls []string
	fail  bool
}

func (f *fakeFetcher) FetchVerified(_ context.Context, source, version string, p config.Platform, destDir string) (*registry.Artifact, error) {
	f.calls = append(f.calls, source+" "+version+" "+p.String())
	if f.fail {
		return nil, errors.New("induced fetch failure")
	}
	typ := source[strings.LastIndex(source, "/")+1:]
	name := fmt.Sprintf("terraform-provider-%s_%s_%s.zip", typ, version, p)
	path := filepath.Join(destDir, name)
	if err := os.WriteFile(path, []byte("zip "+name), 0o600); err != nil {
		return nil, err
	}
	return &registry.Artifact{
		Source:       source,
		Version:      version,
		Platform:     p,
		Path:         path,
		Filename:     name,
		SHA256:       "sha-" + name,
		H1:           "h1:" + name,
		SigningKeyID: "34365D9472D7468F",
	}, nil
}

func newTestApplier(t *testing.T, b *fakeBucket) (*Applier, *fakeFetcher, *bytes.Buffer) {
	t.Helper()

	f := &fakeFetcher{}
	s, _ := newTestSigner(t, "", "v2.6.1")
	var logBuf bytes.Buffer
	a := NewApplier(b, f, s, slog.New(slog.NewJSONHandler(&logBuf, nil)))
	return a, f, &logBuf
}

// nullState builds a desired-state literal for the null provider.
func nullState(versions map[string][]string) mirror.State {
	ps := make(mirror.ProviderState, len(versions))
	for v, plats := range versions {
		set := make(mirror.PlatformSet, len(plats))
		for _, p := range plats {
			set[p] = struct{}{}
		}
		ps[v] = set
	}
	return mirror.State{nullSrc: ps}
}

// planAgainst reads actual state from the bucket and diffs desired
// against it — the same read-diff step runApply performs.
func planAgainst(t *testing.T, b *fakeBucket, desired mirror.State) (*mirror.Plan, *Actual) {
	t.Helper()

	declared := slices.Collect(maps.Keys(desired))
	actual, err := ReadActual(context.Background(), b, declared)
	if err != nil {
		t.Fatalf("ReadActual() error: %v", err)
	}
	plan, err := mirror.Diff(desired, actual.State)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	return plan, actual
}

const nullSrc = "registry.terraform.io/hashicorp/null"

func TestApplyFirstPublish(t *testing.T) {
	t.Parallel()

	b := newFakeBucket()
	a, ff, logBuf := newTestApplier(t, b)
	desired := nullState(map[string][]string{"3.2.4": {"darwin_arm64", "linux_amd64"}})
	plan, actual := planAgainst(t, b, desired)

	if err := a.Apply(context.Background(), plan, actual); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}

	// Both platforms fetched and published: zip + sig + pem + intoto
	// each, one version doc, one index.
	if len(ff.calls) != 2 {
		t.Errorf("fetch calls = %v, want 2", ff.calls)
	}
	wantKeys := []string{
		nullSrc + "/3.2.4.json",
		nullSrc + "/index.json",
		nullSrc + "/terraform-provider-null_3.2.4_darwin_arm64.zip",
		nullSrc + "/terraform-provider-null_3.2.4_darwin_arm64.zip.intoto.jsonl",
		nullSrc + "/terraform-provider-null_3.2.4_darwin_arm64.zip.pem",
		nullSrc + "/terraform-provider-null_3.2.4_darwin_arm64.zip.sig",
		nullSrc + "/terraform-provider-null_3.2.4_linux_amd64.zip",
		nullSrc + "/terraform-provider-null_3.2.4_linux_amd64.zip.intoto.jsonl",
		nullSrc + "/terraform-provider-null_3.2.4_linux_amd64.zip.pem",
		nullSrc + "/terraform-provider-null_3.2.4_linux_amd64.zip.sig",
	}
	keys, _ := b.List(context.Background(), "")
	if !slices.Equal(keys, wantKeys) {
		t.Errorf("bucket keys = %v,\nwant %v", keys, wantKeys)
	}

	// The index is the commit point: written last, exactly once.
	if last := b.ops[len(b.ops)-1]; last != "put "+nullSrc+"/index.json" {
		t.Errorf("last op = %q, want the index write", last)
	}
	idxBody, _, err := b.Get(context.Background(), indexKey(nullSrc))
	if err != nil {
		t.Fatalf("reading index: %v", err)
	}
	if got := string(idxBody); got != `{"versions":{"3.2.4":{}}}` {
		t.Errorf("index = %s", got)
	}

	// Version doc references both archives by filename with the h1.
	docBody, _, err := b.Get(context.Background(), versionKey(nullSrc, "3.2.4"))
	if err != nil {
		t.Fatalf("reading version doc: %v", err)
	}
	var doc mirror.VersionDoc
	if err := json.Unmarshal(docBody, &doc); err != nil {
		t.Fatalf("parsing version doc: %v", err)
	}
	linux := doc.Archives["linux_amd64"]
	if linux.URL != "terraform-provider-null_3.2.4_linux_amd64.zip" ||
		len(linux.Hashes) != 1 || !strings.HasPrefix(linux.Hashes[0], "h1:") {
		t.Errorf("linux archive = %+v", linux)
	}

	// Two publish audit lines.
	if got := strings.Count(logBuf.String(), `"action":"publish"`); got != 2 {
		t.Errorf("publish audit lines = %d, want 2\n%s", got, logBuf.String())
	}

	// A follow-up plan is empty: the mirror converged.
	plan2, _ := planAgainst(t, b, desired)
	if !plan2.Empty() {
		t.Errorf("post-apply plan not empty: %+v", plan2)
	}
}

func TestApplyAddPlatformMergesDoc(t *testing.T) {
	t.Parallel()

	b := newFakeBucket()
	b.seed(indexKey(nullSrc), []byte(`{"versions":{"3.2.4":{}}}`))
	b.seed(versionKey(nullSrc, "3.2.4"),
		[]byte(`{"archives":{"linux_amd64":{"url":"existing.zip","hashes":["h1:existing"]}}}`))

	a, _, _ := newTestApplier(t, b)
	desired := nullState(map[string][]string{"3.2.4": {"darwin_arm64", "linux_amd64"}})
	plan, actual := planAgainst(t, b, desired)
	if len(plan.AddPlatform) != 1 {
		t.Fatalf("plan = %+v, want one add_platform", plan)
	}

	if err := a.Apply(context.Background(), plan, actual); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}

	docBody, _, _ := b.Get(context.Background(), versionKey(nullSrc, "3.2.4"))
	var doc mirror.VersionDoc
	if err := json.Unmarshal(docBody, &doc); err != nil {
		t.Fatalf("parsing merged doc: %v", err)
	}
	// The pre-existing platform entry is preserved verbatim; the new
	// one is added.
	if got := doc.Archives["linux_amd64"]; got.URL != "existing.zip" || got.Hashes[0] != "h1:existing" {
		t.Errorf("existing archive rewritten: %+v", got)
	}
	if _, ok := doc.Archives["darwin_arm64"]; !ok {
		t.Error("new platform missing from merged doc")
	}
}

func TestApplyRemove(t *testing.T) {
	t.Parallel()

	b := newFakeBucket()
	b.seed(indexKey(nullSrc), []byte(`{"versions":{"3.2.3":{},"3.2.4":{}}}`))
	b.seed(versionKey(nullSrc, "3.2.3"),
		[]byte(`{"archives":{"linux_amd64":{"url":"old.zip","hashes":["h1:old"]}}}`))
	b.seed(versionKey(nullSrc, "3.2.4"),
		[]byte(`{"archives":{"linux_amd64":{"url":"cur.zip","hashes":["h1:cur"]}}}`))
	b.seed(zipKey(nullSrc, "old.zip"), []byte("old zip bytes"))

	a, ff, logBuf := newTestApplier(t, b)
	desired := nullState(map[string][]string{"3.2.4": {"linux_amd64"}})
	plan, actual := planAgainst(t, b, desired)
	if len(plan.Remove) != 1 {
		t.Fatalf("plan = %+v, want one remove", plan)
	}

	if err := a.Apply(context.Background(), plan, actual); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}

	// Removal fetches nothing.
	if len(ff.calls) != 0 {
		t.Errorf("fetch calls = %v, want none", ff.calls)
	}
	// Index rewritten BEFORE the doc delete: consumers never see a
	// listed-but-missing version document.
	idxOp := slices.Index(b.ops, "put "+indexKey(nullSrc))
	delOp := slices.Index(b.ops, "delete "+versionKey(nullSrc, "3.2.3"))
	if idxOp == -1 || delOp == -1 || idxOp > delOp {
		t.Errorf("ops = %v, want index rewrite before doc delete", b.ops)
	}
	idxBody, _, _ := b.Get(context.Background(), indexKey(nullSrc))
	if got := string(idxBody); got != `{"versions":{"3.2.4":{}}}` {
		t.Errorf("index = %s", got)
	}
	if _, _, err := b.Get(context.Background(), versionKey(nullSrc, "3.2.3")); !errors.Is(err, ErrNotFound) {
		t.Error("removed version doc still present")
	}
	// The zip stays for forensics.
	if _, _, err := b.Get(context.Background(), zipKey(nullSrc, "old.zip")); err != nil {
		t.Error("zip must never be deleted on retract")
	}
	// One retract audit line carrying the stored h1.
	if got := strings.Count(logBuf.String(), `"action":"retract"`); got != 1 {
		t.Errorf("retract audit lines = %d, want 1", got)
	}
	if !strings.Contains(logBuf.String(), `"h1":"h1:old"`) {
		t.Errorf("retract line missing stored h1: %s", logBuf.String())
	}
}

func TestApplyConflictOnConcurrentIndexWrite(t *testing.T) {
	t.Parallel()

	b := newFakeBucket()
	b.seed(indexKey(nullSrc), []byte(`{"versions":{"3.2.3":{}}}`))
	b.seed(versionKey(nullSrc, "3.2.3"),
		[]byte(`{"archives":{"linux_amd64":{"url":"old.zip","hashes":["h1:old"]}}}`))

	a, _, _ := newTestApplier(t, b)
	desired := nullState(map[string][]string{
		"3.2.3": {"linux_amd64"},
		"3.2.4": {"linux_amd64"},
	})
	plan, actual := planAgainst(t, b, desired)

	// A concurrent writer lands between the read and the apply.
	b.seed(indexKey(nullSrc), []byte(`{"versions":{"3.2.3":{},"9.9.9":{}}}`))

	err := a.Apply(context.Background(), plan, actual)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply() error = %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "re-run sluice plan") {
		t.Errorf("conflict message missing re-plan instruction: %v", err)
	}
	// The concurrent writer's index survives untouched.
	idxBody, _, _ := b.Get(context.Background(), indexKey(nullSrc))
	if !strings.Contains(string(idxBody), "9.9.9") {
		t.Errorf("concurrent index overwritten: %s", idxBody)
	}
}

func TestApplyFirstPublishRace(t *testing.T) {
	t.Parallel()

	b := newFakeBucket()
	a, _, _ := newTestApplier(t, b)
	desired := nullState(map[string][]string{"3.2.4": {"linux_amd64"}})
	plan, actual := planAgainst(t, b, desired)

	// Someone publishes the provider's first index concurrently.
	b.seed(indexKey(nullSrc), []byte(`{"versions":{"1.0.0":{}}}`))

	if err := a.Apply(context.Background(), plan, actual); !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply() error = %v, want ErrConflict on If-None-Match race", err)
	}
}

// TestApplyIdempotentRetry is Phase 4's convergence proof: a rerun
// after an induced mid-apply failure republishes staged artifacts
// and converges.
func TestApplyIdempotentRetry(t *testing.T) {
	t.Parallel()

	b := newFakeBucket()
	a, _, _ := newTestApplier(t, b)
	desired := nullState(map[string][]string{"3.2.4": {"darwin_arm64", "linux_amd64"}})
	plan, actual := planAgainst(t, b, desired)

	// 2 artifacts × (zip + sig + pem + intoto) = 8 puts, then the
	// version doc is the 9th — fail there, before the index.
	b.failPutsAfter = 8
	if err := a.Apply(context.Background(), plan, actual); err == nil {
		t.Fatal("Apply() error = nil, want induced failure")
	}
	// The index never landed: observable state is unchanged and the
	// next plan is identical.
	if _, _, err := b.Get(context.Background(), indexKey(nullSrc)); !errors.Is(err, ErrNotFound) {
		t.Fatal("index written despite mid-apply failure")
	}
	plan2, actual2 := planAgainst(t, b, desired)
	if plan2.Empty() {
		t.Fatal("post-crash plan empty; convergence test is vacuous")
	}

	// The rerun converges.
	b.failPutsAfter = -1
	if err := a.Apply(context.Background(), plan2, actual2); err != nil {
		t.Fatalf("rerun Apply() unexpected error: %v", err)
	}
	plan3, _ := planAgainst(t, b, desired)
	if !plan3.Empty() {
		t.Errorf("plan after rerun not empty: %+v", plan3)
	}
}

func TestApplyPreflightFailureTouchesNothing(t *testing.T) {
	t.Parallel()

	b := newFakeBucket()
	f := &fakeFetcher{}
	s, _ := newTestSigner(t, "", "v1.13.7") // below the floor
	a := NewApplier(b, f, s, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))

	desired := nullState(map[string][]string{"3.2.4": {"linux_amd64"}})
	plan, actual := planAgainst(t, b, desired)

	err := a.Apply(context.Background(), plan, actual)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Apply() error = %v, want cosign preflight failure", err)
	}
	if len(f.calls) != 0 || len(b.ops) != 0 {
		t.Errorf("preflight failure must precede all work: fetches=%v ops=%v", f.calls, b.ops)
	}
}

func TestApplyEmptyPlanIsNoOp(t *testing.T) {
	t.Parallel()

	b := newFakeBucket()
	a, ff, _ := newTestApplier(t, b)
	if err := a.Apply(context.Background(), &mirror.Plan{}, &Actual{}); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}
	if len(ff.calls) != 0 || len(b.ops) != 0 {
		t.Error("empty plan must touch nothing")
	}
}

// TestAuditSchema freezes the audit line's wire schema — a contract
// for downstream log pipelines.
func TestAuditSchema(t *testing.T) {
	t.Parallel()

	b := newFakeBucket()
	a, _, logBuf := newTestApplier(t, b)
	desired := nullState(map[string][]string{"3.2.4": {"linux_amd64"}})
	plan, actual := planAgainst(t, b, desired)
	if err := a.Apply(context.Background(), plan, actual); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d, want 1", len(lines))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("audit line is not JSON: %v", err)
	}
	want := []string{
		"time", "level", "msg",
		"action", "provider", "version", "platform",
		"h1", "sha256", "signing_key_id", "s3_version_id",
	}
	got := slices.Sorted(maps.Keys(entry))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("audit keys = %v, want %v", got, want)
	}
	if entry["action"] != "publish" || entry["provider"] != nullSrc {
		t.Errorf("audit content = %v", entry)
	}
	if entry["s3_version_id"] == "" {
		t.Error("s3_version_id empty on a versioned fake")
	}
}
