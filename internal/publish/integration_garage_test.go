// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

//go:build integration

package publish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// garageBackend returns the real S3 adapter against a Garage bucket,
// configured exactly like production wiring for non-AWS endpoints
// (BaseEndpoint + UsePathStyle + checksums only when required).
// SLUICE_GARAGE_ENDPOINT, SLUICE_GARAGE_ACCESS_KEY,
// SLUICE_GARAGE_SECRET_KEY, and SLUICE_GARAGE_BUCKET must all be set;
// otherwise the test skips. The bucket must pre-exist with read+write
// granted to the key — Garage has no self-service bucket creation for
// plain keys.
func garageBackend(t *testing.T) Bucket {
	t.Helper()

	endpoint := os.Getenv("SLUICE_GARAGE_ENDPOINT")
	accessKey := os.Getenv("SLUICE_GARAGE_ACCESS_KEY")
	secretKey := os.Getenv("SLUICE_GARAGE_SECRET_KEY")
	bucket := os.Getenv("SLUICE_GARAGE_BUCKET")
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		t.Skipf("Garage backend not configured — set SLUICE_GARAGE_ENDPOINT/ACCESS_KEY/SECRET_KEY/BUCKET")
	}

	ctx := context.Background()
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("garage"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		t.Fatalf("loading AWS config: %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return NewS3(client, bucket)
}

// garagePrefix isolates one test's keys. Depth keeps every key clear
// of source discovery — and no test here writes an index.json-shaped
// key, so nothing here can ever read as a provider.
func garagePrefix(t *testing.T) string {
	t.Helper()

	return fmt.Sprintf("_sluice/garage-it-%s-%d/",
		strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-")), time.Now().UnixNano())
}

func TestGarageProbeDegraded(t *testing.T) {
	// No t.Parallel: shared bucket, and the assertion below is a
	// tripwire — if Garage ever gains versioning and preconditions,
	// this fails on purpose so the degraded paths get revisited.
	b := garageBackend(t)

	caps, err := Probe(context.Background(), b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caps.Mode != ModeDegraded {
		t.Fatalf("mode = %q, want degraded (Garage capability change?)", caps.Mode)
	}
}

func TestGarageObjectRoundTrip(t *testing.T) {
	t.Parallel()

	b := garageBackend(t)
	ctx := context.Background()
	prefix := garagePrefix(t)
	key := prefix + "roundtrip.json"
	body := []byte(`{"hello":"garage"}`)

	if _, err := b.Put(ctx, key, "application/json", body, Cond{}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	got, etag, err := b.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("Get() = %q, want %q", got, body)
	}
	t.Logf("ETag observed: %q", etag)
	if _, err := b.Delete(ctx, key); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, _, err := b.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after delete = %v, want ErrNotFound", err)
	}
}

func TestGarageConditionalWritesIgnored(t *testing.T) {
	t.Parallel()

	// Pins the accepted degraded gap at the live tier: Garage
	// answers 200 while ignoring preconditions, which is exactly why
	// apply fails closed without --allow-unversioned-backend.
	b := garageBackend(t)
	ctx := context.Background()
	key := garagePrefix(t) + "cond.json"

	if _, err := b.Put(ctx, key, "application/json", []byte("v1"), Cond{IfNoneMatch: true}); err != nil {
		t.Fatalf("first Put() error = %v", err)
	}
	if _, err := b.Put(ctx, key, "application/json", []byte("v2"), Cond{IfNoneMatch: true}); err != nil {
		t.Fatalf("repeated If-None-Match Put() error = %v, want silent overwrite (Garage ignores preconditions)", err)
	}
	if _, err := b.Put(ctx, key, "application/json", []byte("v3"), Cond{IfMatch: "bogus"}); err != nil {
		t.Fatalf("bogus If-Match Put() error = %v, want silent overwrite (Garage ignores preconditions)", err)
	}
	if _, err := b.Delete(ctx, key); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}
