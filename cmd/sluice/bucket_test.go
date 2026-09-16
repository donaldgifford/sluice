// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/donaldgifford/sluice/internal/config"
)

func bucketManifest(endpoint string) *config.Manifest {
	return &config.Manifest{
		Mirror: config.Mirror{
			Bucket:    "b",
			Region:    "us-east-1",
			Endpoint:  endpoint,
			PathStyle: endpoint != "",
		},
	}
}

func TestNewBucketEndpointWiring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint string
		wantErr  string
	}{
		{
			name:     "empty endpoint takes the AWS path",
			endpoint: "",
		},
		{
			name:     "https endpoint constructs",
			endpoint: "https://s3.internal",
		},
		{
			name:     "http loopback name constructs",
			endpoint: "http://localhost:4566",
		},
		{
			name:     "http loopback IP constructs",
			endpoint: "http://127.0.0.1:9000",
		},
		{
			name:     "http non-loopback refused",
			endpoint: "http://s3.internal",
			wantErr:  `refusing non-TLS S3 endpoint "http://s3.internal"`,
		},
		{
			name:     "non-URL refused",
			endpoint: "notaurl",
			wantErr:  `is not a valid http(s) URL`,
		},
		{
			name:     "non-http scheme refused",
			endpoint: "ftp://s3.internal",
			wantErr:  `is not a valid http(s) URL`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Construction performs no I/O: LoadDefaultConfig resolves
			// lazily and the client dials on first request.
			b, err := newBucket(context.Background(), bucketManifest(tt.endpoint))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if b == nil {
					t.Fatal("expected a bucket, got nil")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestBackendResolveRejectsBadEndpoint(t *testing.T) {
	// t.Setenv: no t.Parallel.
	t.Setenv("SLUICE_S3_ENDPOINT", "notaurl")
	t.Setenv("SLUICE_S3_PATH_STYLE", "true")

	_, bf := backendCmd(t)
	if err := bf.apply(backendManifest("", false)); err == nil ||
		!strings.Contains(err.Error(), "is not a valid http(s) URL") {
		t.Fatalf("error = %v, want invalid-URL substring", err)
	}
}
