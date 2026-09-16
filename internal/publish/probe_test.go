// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

// probeFake is a Bucket with independently switchable S3 semantics:
// honorConds mirrors AWS conditional writes (ErrConflict on
// violation), versioned mirrors versioned buckets (Put returns a
// version ID). The four combinations cover Full, Garage-shaped
// Degraded, and both half-capable tiers. failPut injects an I/O
// failure for error-propagation tests.
type probeFake struct {
	mu         sync.Mutex
	objects    map[string][]byte
	honorConds bool
	versioned  bool
	failPut    error
	nextVer    int
}

func newProbeFake(honorConds, versioned bool) *probeFake {
	return &probeFake{objects: make(map[string][]byte), honorConds: honorConds, versioned: versioned}
}

func (f *probeFake) Get(_ context.Context, key string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, ok := f.objects[key]
	if !ok {
		return nil, "", fmt.Errorf("fake get %s: %w", key, ErrNotFound)
	}
	return body, `"etag"`, nil
}

func (f *probeFake) Put(_ context.Context, key, _ string, body []byte, cond Cond) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPut != nil {
		return "", f.failPut
	}
	_, exists := f.objects[key]
	if f.honorConds {
		if cond.IfNoneMatch && exists {
			return "", fmt.Errorf("fake put %s: %w", key, ErrConflict)
		}
		if cond.IfMatch != "" && (!exists || cond.IfMatch != `"etag"`) {
			return "", fmt.Errorf("fake put %s: %w", key, ErrConflict)
		}
	}
	f.objects[key] = append([]byte(nil), body...)
	if !f.versioned {
		return "", nil
	}
	f.nextVer++
	return fmt.Sprintf("v%03d", f.nextVer), nil
}

func (f *probeFake) Delete(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return "", nil
}

func (f *probeFake) List(_ context.Context, prefix string) ([]string, error) {
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

var _ Bucket = (*probeFake)(nil)

func TestProbeModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		honorConds bool
		versioned  bool
		want       Mode
	}{
		{name: "honoring versioned is full", honorConds: true, versioned: true, want: ModeFull},
		{name: "ignoring unversioned is degraded", honorConds: false, versioned: false, want: ModeDegraded},
		{name: "honoring unversioned is degraded", honorConds: true, versioned: false, want: ModeDegraded},
		{name: "versioned ignoring is degraded", honorConds: false, versioned: true, want: ModeDegraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			caps, err := Probe(context.Background(), newProbeFake(tt.honorConds, tt.versioned))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if caps.Mode != tt.want {
				t.Fatalf("mode = %q, want %q", caps.Mode, tt.want)
			}
			if caps.Versioned != tt.versioned {
				t.Errorf("Versioned = %v, want %v", caps.Versioned, tt.versioned)
			}
			if caps.Conditionals != tt.honorConds {
				t.Errorf("Conditionals = %v, want %v", caps.Conditionals, tt.honorConds)
			}
		})
	}
}

func TestProbeSummaries(t *testing.T) {
	t.Parallel()

	if got := (&Capabilities{Mode: ModeFull}).Summary(); got != "full" {
		t.Errorf("full summary = %q, want %q", got, "full")
	}
	got := (&Capabilities{Mode: ModeDegraded}).Summary()
	for _, want := range []string{"degraded", "no versioning", "preconditions ignored"} {
		if !strings.Contains(got, want) {
			t.Errorf("degraded summary = %q, want substring %q", got, want)
		}
	}
}

func TestProbeCleansUpScratchKeys(t *testing.T) {
	t.Parallel()

	f := newProbeFake(false, false)
	if _, err := Probe(context.Background(), f); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	keys, err := f.List(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, k := range keys {
		if strings.HasPrefix(k, probePrefix) {
			t.Errorf("scratch key %q left behind", k)
		}
	}
}

func TestProbePropagatesIOFailures(t *testing.T) {
	t.Parallel()

	f := newProbeFake(true, true)
	f.failPut = errors.New("boom")
	if _, err := Probe(context.Background(), f); err == nil ||
		!strings.Contains(err.Error(), "probing backend") {
		t.Fatalf("error = %v, want probing-backend failure", err)
	}
}
