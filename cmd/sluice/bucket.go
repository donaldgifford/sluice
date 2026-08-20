// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"context"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/publish"
)

// newBucket builds the production S3 bucket from the manifest's
// mirror block, using the default AWS credential chain.
func newBucket(ctx context.Context, m *config.Manifest) (publish.Bucket, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(m.Mirror.Region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return publish.NewS3(s3.NewFromConfig(cfg), m.Mirror.Bucket), nil
}
