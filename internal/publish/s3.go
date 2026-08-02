// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// s3Bucket adapts aws-sdk-go-v2 to [Bucket]. Deliberately thin: all
// publish semantics live above the interface; LocalStack integration
// coverage lands in Phase 5.
type s3Bucket struct {
	client *s3.Client
	bucket string
}

var _ Bucket = (*s3Bucket)(nil)

// NewS3 returns a Bucket backed by the given S3 client and bucket.
func NewS3(client *s3.Client, bucket string) Bucket {
	return &s3Bucket{client: client, bucket: bucket}
}

func (b *s3Bucket) Get(ctx context.Context, key string) ([]byte, string, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noKey *types.NoSuchKey
		if errors.As(err, &noKey) {
			return nil, "", fmt.Errorf("s3 get %s: %w", key, ErrNotFound)
		}
		return nil, "", fmt.Errorf("s3 get %s: %w", key, err)
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", fmt.Errorf("s3 get %s: reading body: %w", key, err)
	}
	return body, aws.ToString(out.ETag), nil
}

func (b *s3Bucket) Put(ctx context.Context, key, contentType string, body []byte, cond Cond) (string, error) {
	in := &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	}
	if cond.IfMatch != "" {
		in.IfMatch = aws.String(cond.IfMatch)
	}
	if cond.IfNoneMatch {
		in.IfNoneMatch = aws.String("*")
	}
	out, err := b.client.PutObject(ctx, in)
	if err != nil {
		if isPreconditionFailure(err) {
			return "", fmt.Errorf("s3 put %s: %w", key, ErrConflict)
		}
		return "", fmt.Errorf("s3 put %s: %w", key, err)
	}
	return aws.ToString(out.VersionId), nil
}

func (b *s3Bucket) Delete(ctx context.Context, key string) (string, error) {
	out, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", fmt.Errorf("s3 delete %s: %w", key, err)
	}
	return aws.ToString(out.VersionId), nil
}

func (b *s3Bucket) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	p := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.bucket),
		Prefix: aws.String(prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3 list %q: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
	}
	return keys, nil
}

// isPreconditionFailure matches both shapes S3 uses for a failed
// conditional write: 412 PreconditionFailed (lost If-Match race) and
// 409 ConditionalRequestConflict (concurrent conditional writers).
func isPreconditionFailure(err error) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	code := ae.ErrorCode()
	return code == "PreconditionFailed" || code == "ConditionalRequestConflict"
}
