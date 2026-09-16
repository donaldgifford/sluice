// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"context"
	"fmt"
	"net"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/publish"
)

// newBucket builds the production S3 bucket from the manifest's
// mirror block, using the default AWS credential chain. A configured
// endpoint selects an S3-compatible backend (DESIGN-0005): path-style
// addressing with SDK checksums disabled, and non-TLS endpoints are
// refused outside loopback/test hosts.
func newBucket(ctx context.Context, m *config.Manifest) (publish.Bucket, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(m.Mirror.Region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	if m.Mirror.Endpoint == "" {
		return publish.NewS3(s3.NewFromConfig(cfg), m.Mirror.Bucket), nil
	}
	u, err := url.Parse(m.Mirror.Endpoint)
	if err != nil || (u.Scheme != httpScheme && u.Scheme != httpsScheme) || u.Host == "" {
		return nil, fmt.Errorf("endpoint %q is not a valid http(s) URL; give the S3 API base URL or omit endpoint for AWS", m.Mirror.Endpoint)
	}
	if u.Scheme == httpScheme && !isLoopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("refusing non-TLS S3 endpoint %q outside loopback/test hosts", m.Mirror.Endpoint)
	}
	return publish.NewS3(s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(m.Mirror.Endpoint)
		o.UsePathStyle = true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	}), m.Mirror.Bucket), nil
}

// isLoopbackHost reports whether host is a loopback name or address —
// the only hosts a non-TLS endpoint is allowed on.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	return net.ParseIP(host).IsLoopback()
}
