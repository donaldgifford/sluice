// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"context"
	"errors"
)

// Sentinel errors for bucket operations.
var (
	// ErrNotFound reports an absent key. A missing index.json means
	// an unmirrored provider (first publish), not a failure.
	ErrNotFound = errors.New("object not found")

	// ErrConflict reports a failed conditional write: the index
	// changed between plan and apply. Maps to exit 3.
	ErrConflict = errors.New("conditional write failed")
)

// Cond constrains a [Bucket.Put]. The zero value is unconditional.
type Cond struct {
	// IfMatch writes only if the object's current ETag matches.
	IfMatch string
	// IfNoneMatch writes only if the key does not exist.
	IfNoneMatch bool
}

// Bucket is the minimal S3 surface the publisher needs. Implemented
// by the aws-sdk-go-v2 adapter in production and a fake in tests.
type Bucket interface {
	// Get returns the object body and ETag; ErrNotFound if absent.
	Get(ctx context.Context, key string) (body []byte, etag string, err error)
	// Put writes the object and returns the S3 version ID ("" on
	// unversioned buckets). A failed Cond returns ErrConflict.
	Put(ctx context.Context, key, contentType string, body []byte, cond Cond) (versionID string, err error)
	// Delete removes the object and returns the delete-marker
	// version ID ("" when unversioned).
	Delete(ctx context.Context, key string) (versionID string, err error)
	// List returns all keys under prefix, across pages.
	List(ctx context.Context, prefix string) ([]string, error)
}
