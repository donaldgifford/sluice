// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

//go:build integration

package publish

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// localstackBucket returns the real S3 adapter against a fresh
// versioned LocalStack bucket, skipping when LocalStack is not
// reachable (CI provides it as a service container; locally, start
// it or set SLUICE_S3_ENDPOINT). SLUICE_S3_ENDPOINT wins over the
// legacy SLUICE_LOCALSTACK_ENDPOINT so the same suite runs against
// any S3-compatible backend (e.g. Garage, degraded profile).
func localstackBucket(t *testing.T) Bucket {
	t.Helper()

	endpoint := os.Getenv("SLUICE_S3_ENDPOINT")
	if endpoint == "" {
		endpoint = os.Getenv("SLUICE_LOCALSTACK_ENDPOINT")
	}
	if endpoint == "" {
		endpoint = "http://localhost:4566"
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parsing endpoint %q: %v", endpoint, err)
	}
	ctx := context.Background()
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		// Locally an absent backend is a skip; in CI the service
		// container is supposed to be there, so a silent skip would
		// turn the job into a green no-op.
		if os.Getenv("CI") != "" {
			t.Fatalf("S3 backend not reachable at %s in CI: %v", endpoint, err)
		}
		t.Skipf("S3 backend not reachable at %s — start it or set SLUICE_S3_ENDPOINT: %v", endpoint, err)
	}
	_ = conn.Close()

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("loading AWS config: %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	// Test name in the bucket keeps parallel tests from colliding on
	// the nanosecond clock and self-documents orphans when debugging.
	name := fmt.Sprintf("sluice-it-%s-%d",
		strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-")), time.Now().UnixNano())
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(name)}); err != nil {
		t.Fatalf("creating bucket: %v", err)
	}
	// Versioned, matching the production bucket design — version IDs
	// must flow into the audit trail.
	if _, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(name),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	}); err != nil {
		t.Fatalf("enabling versioning: %v", err)
	}
	return NewS3(client, name)
}

func TestIntegrationAdapter(t *testing.T) {
	t.Parallel()

	b := localstackBucket(t)
	ctx := context.Background()

	t.Run("get of a missing key is ErrNotFound", func(t *testing.T) {
		t.Parallel()

		if _, _, err := b.Get(ctx, "absent/key.json"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("put get roundtrip with etag and version id", func(t *testing.T) {
		t.Parallel()

		vid, err := b.Put(ctx, "a/b/c/obj.json", "application/json", []byte(`{"x":1}`), Cond{})
		if err != nil {
			t.Fatalf("Put() error: %v", err)
		}
		if vid == "" {
			t.Error("Put() version ID empty on a versioned bucket")
		}
		body, etag, err := b.Get(ctx, "a/b/c/obj.json")
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if string(body) != `{"x":1}` || etag == "" {
			t.Errorf("Get() = %q etag %q", body, etag)
		}
	})

	t.Run("list returns keys under prefix", func(t *testing.T) {
		t.Parallel()

		for _, k := range []string{"p/x/1", "p/x/2", "q/y/1"} {
			if _, err := b.Put(ctx, k, "text/plain", []byte("v"), Cond{}); err != nil {
				t.Fatalf("Put(%s) error: %v", k, err)
			}
		}
		keys, err := b.List(ctx, "p/")
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(keys) != 2 || keys[0] != "p/x/1" || keys[1] != "p/x/2" {
			t.Errorf("List(p/) = %v", keys)
		}
	})

	t.Run("delete removes and returns a marker version", func(t *testing.T) {
		t.Parallel()

		if _, err := b.Put(ctx, "del/me", "text/plain", []byte("v"), Cond{}); err != nil {
			t.Fatalf("Put() error: %v", err)
		}
		vid, err := b.Delete(ctx, "del/me")
		if err != nil {
			t.Fatalf("Delete() error: %v", err)
		}
		if vid == "" {
			t.Error("Delete() marker version empty on a versioned bucket")
		}
		if _, _, err := b.Get(ctx, "del/me"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get() after delete = %v, want ErrNotFound", err)
		}
	})
}

// TestIntegrationConditionalWrites proves the adapter's ErrConflict
// mapping against real S3 API semantics — the same call sites the
// fake bucket models.
func TestIntegrationConditionalWrites(t *testing.T) {
	t.Parallel()

	b := localstackBucket(t)
	ctx := context.Background()
	const key = "prov/ns/type/index.json"

	// First publish: If-None-Match succeeds once.
	if _, err := b.Put(ctx, key, "application/json", []byte(`{"versions":{}}`), Cond{IfNoneMatch: true}); err != nil {
		t.Fatalf("first If-None-Match Put: %v", err)
	}
	// A second If-None-Match loses the race.
	if _, err := b.Put(ctx, key, "application/json", []byte(`{}`), Cond{IfNoneMatch: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second If-None-Match Put = %v, want ErrConflict", err)
	}

	_, etag, err := b.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	// If-Match with the current ETag succeeds…
	if _, err := b.Put(ctx, key, "application/json", []byte(`{"versions":{"1.0.0":{}}}`), Cond{IfMatch: etag}); err != nil {
		t.Fatalf("If-Match Put with current etag: %v", err)
	}
	// …and the now-stale ETag conflicts.
	if _, err := b.Put(ctx, key, "application/json", []byte(`{}`), Cond{IfMatch: etag}); !errors.Is(err, ErrConflict) {
		t.Fatalf("If-Match Put with stale etag = %v, want ErrConflict", err)
	}
}

// TestIntegrationApplyLifecycle is Phase 4's success criteria run
// against LocalStack: first publish, deterministic re-plan,
// concurrent-writer conflict with an intact index, and removal.
func TestIntegrationApplyLifecycle(t *testing.T) {
	t.Parallel()

	b := localstackBucket(t)
	ctx := context.Background()

	// First publish.
	desired := nullState(map[string][]string{"3.2.4": {"darwin_arm64", "linux_amd64"}})
	plan, actual := planAgainst(t, b, desired)
	a, _, logBuf := newTestApplier(t, b)
	if err := a.Apply(ctx, plan, actual); err != nil {
		t.Fatalf("first Apply() error: %v", err)
	}
	if got := strings.Count(logBuf.String(), `"action":"publish"`); got != 2 {
		t.Errorf("publish audit lines = %d, want 2", got)
	}
	if !strings.Contains(logBuf.String(), `"s3_version_id":"`) {
		t.Error("audit lines missing real S3 version IDs")
	}

	// Determinism: an immediate re-plan is empty.
	replan, _ := planAgainst(t, b, desired)
	if !replan.Empty() {
		t.Fatalf("re-plan after apply not empty: %+v", replan)
	}

	// Concurrent writer: apply with a stale read observes ErrConflict
	// and leaves the interloper's index intact.
	desired2 := nullState(map[string][]string{
		"3.2.4": {"darwin_arm64", "linux_amd64"},
		"3.3.0": {"linux_amd64"},
	})
	plan2, actual2 := planAgainst(t, b, desired2)
	if _, err := b.Put(ctx, indexKey(nullSrc), "application/json",
		[]byte(`{"versions":{"3.2.4":{},"9.9.9":{}}}`), Cond{}); err != nil {
		t.Fatalf("interloper Put: %v", err)
	}
	a2, _, _ := newTestApplier(t, b)
	if err := a2.Apply(ctx, plan2, actual2); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Apply() = %v, want ErrConflict", err)
	}
	idxBody, _, err := b.Get(ctx, indexKey(nullSrc))
	if err != nil {
		t.Fatalf("Get(index) error: %v", err)
	}
	if !strings.Contains(string(idxBody), "9.9.9") {
		t.Errorf("interloper index overwritten: %s", idxBody)
	}

	// Removal: converge to a smaller set from a fresh read (9.9.9 has
	// no version doc, so drop the interloper's entry first the way an
	// operator would — restore a sluice-authored index, then remove).
	if _, err := b.Put(ctx, indexKey(nullSrc), "application/json",
		[]byte(`{"versions":{"3.2.4":{}}}`), Cond{}); err != nil {
		t.Fatalf("restoring index: %v", err)
	}
	desired3 := nullState(map[string][]string{})
	plan3, actual3 := planAgainst(t, b, desired3)
	if len(plan3.Remove) != 1 {
		t.Fatalf("removal plan = %+v, want one remove", plan3)
	}
	a3, _, logBuf3 := newTestApplier(t, b)
	if err := a3.Apply(ctx, plan3, actual3); err != nil {
		t.Fatalf("removal Apply() error: %v", err)
	}
	if _, _, err := b.Get(ctx, versionKey(nullSrc, "3.2.4")); !errors.Is(err, ErrNotFound) {
		t.Error("removed version doc still present")
	}
	// Zips are never deleted.
	if _, _, err := b.Get(ctx, zipKey(nullSrc, "terraform-provider-null_3.2.4_linux_amd64.zip")); err != nil {
		t.Error("zip deleted on retract")
	}
	if got := strings.Count(logBuf3.String(), `"action":"retract"`); got != 2 {
		t.Errorf("retract audit lines = %d, want 2", got)
	}
}
