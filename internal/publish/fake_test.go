// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"context"
	"crypto/md5" // ETag simulation, not cryptography
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// fakeBucket is an in-memory Bucket with S3-shaped conditional-write
// semantics: ETag is a quoted MD5 of the body (matching S3's
// observable behavior for simple puts), version IDs are a monotonic
// counter. If-Match mismatches and If-None-Match collisions return
// ErrConflict exactly as the real adapter maps 412/409.
type fakeBucket struct {
	mu      sync.Mutex
	objects map[string]fakeObject
	nextVer int

	// ops records mutations in order ("put <key>" / "delete <key>")
	// so tests can assert write ordering — the index must be last.
	ops []string

	// failPutsAfter, when >= 0, fails every Put after that many
	// successes — the mid-apply crash lever.
	failPutsAfter int
	puts          int
}

type fakeObject struct {
	body      []byte
	etag      string
	versionID string
}

func newFakeBucket() *fakeBucket {
	return &fakeBucket{objects: make(map[string]fakeObject), failPutsAfter: -1}
}

func etagOf(body []byte) string {
	sum := md5.Sum(body)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func (f *fakeBucket) Get(_ context.Context, key string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	obj, ok := f.objects[key]
	if !ok {
		return nil, "", fmt.Errorf("fake get %s: %w", key, ErrNotFound)
	}
	return obj.body, obj.etag, nil
}

func (f *fakeBucket) Put(_ context.Context, key, _ string, body []byte, cond Cond) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failPutsAfter >= 0 && f.puts >= f.failPutsAfter {
		return "", fmt.Errorf("fake put %s: induced failure", key)
	}

	cur, exists := f.objects[key]
	if cond.IfNoneMatch && exists {
		return "", fmt.Errorf("fake put %s: %w", key, ErrConflict)
	}
	if cond.IfMatch != "" && (!exists || cur.etag != cond.IfMatch) {
		return "", fmt.Errorf("fake put %s: %w", key, ErrConflict)
	}

	f.puts++
	f.nextVer++
	f.ops = append(f.ops, "put "+key)
	vid := fmt.Sprintf("v%03d", f.nextVer)
	f.objects[key] = fakeObject{body: append([]byte(nil), body...), etag: etagOf(body), versionID: vid}
	return vid, nil
}

func (f *fakeBucket) Delete(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	f.nextVer++
	f.ops = append(f.ops, "delete "+key)
	return fmt.Sprintf("v%03d", f.nextVer), nil
}

func (f *fakeBucket) List(_ context.Context, prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// seed writes an object unconditionally, bypassing failure levers —
// for test arrangement.
func (f *fakeBucket) seed(key string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextVer++
	f.objects[key] = fakeObject{
		body:      append([]byte(nil), body...),
		etag:      etagOf(body),
		versionID: fmt.Sprintf("v%03d", f.nextVer),
	}
}

var _ Bucket = (*fakeBucket)(nil)
