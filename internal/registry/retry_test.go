// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// countingServer serves via handler, which receives the 1-based
// request number.
func countingServer(t *testing.T, handler func(n int64, w http.ResponseWriter)) (*Client, string, *atomic.Int64) {
	t.Helper()

	var count atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /blob", func(w http.ResponseWriter, _ *http.Request) {
		handler(count.Add(1), w)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := New(WithBaseURL(func(string) string { return srv.URL }))
	return c, srv.URL + "/blob", &count
}

func TestRetry(t *testing.T) {
	t.Parallel()

	content := []byte("eventually consistent bytes")

	t.Run("5xx retries until success", func(t *testing.T) {
		t.Parallel()

		c, u, count := countingServer(t, func(n int64, w http.ResponseWriter) {
			if n <= 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(content)
		})
		body, err := c.get(context.Background(), u)
		if err != nil {
			t.Fatalf("get() unexpected error: %v", err)
		}
		if !bytes.Equal(body, content) {
			t.Errorf("body = %q, want %q", body, content)
		}
		if got := count.Load(); got != 3 {
			t.Errorf("request count = %d, want 3", got)
		}
	})

	t.Run("429 retries", func(t *testing.T) {
		t.Parallel()

		c, u, count := countingServer(t, func(n int64, w http.ResponseWriter) {
			if n == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write(content)
		})
		if _, err := c.get(context.Background(), u); err != nil {
			t.Fatalf("get() unexpected error: %v", err)
		}
		if got := count.Load(); got != 2 {
			t.Errorf("request count = %d, want 2", got)
		}
	})

	t.Run("404 does not retry", func(t *testing.T) {
		t.Parallel()

		c, u, count := countingServer(t, func(_ int64, w http.ResponseWriter) {
			w.WriteHeader(http.StatusNotFound)
		})
		_, err := c.get(context.Background(), u)
		var se *statusError
		if !errors.As(err, &se) || se.code != http.StatusNotFound {
			t.Fatalf("get() error = %v, want statusError 404", err)
		}
		if got := count.Load(); got != 1 {
			t.Errorf("request count = %d, want 1 (no retry on 404)", got)
		}
	})

	t.Run("exhausted attempts return the last error", func(t *testing.T) {
		t.Parallel()

		c, u, count := countingServer(t, func(_ int64, w http.ResponseWriter) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		_, err := c.get(context.Background(), u)
		var se *statusError
		if !errors.As(err, &se) || se.code != http.StatusInternalServerError {
			t.Fatalf("get() error = %v, want statusError 500", err)
		}
		if got := count.Load(); got != retryAttempts {
			t.Errorf("request count = %d, want %d", got, retryAttempts)
		}
	})

	t.Run("mid-stream cut retries and converges", func(t *testing.T) {
		t.Parallel()

		c, u, count := countingServer(t, func(n int64, w http.ResponseWriter) {
			if n == 1 {
				// Truncated body: promise more than we send.
				w.Header().Set("Content-Length", "1024")
				_, _ = w.Write(content[:4])
				return
			}
			_, _ = w.Write(content)
		})
		destDir := t.TempDir()
		path, err := c.fetchZipRetry(context.Background(), u, sha256Hex(content), destDir, "p.zip")
		if err != nil {
			t.Fatalf("fetchZipRetry() unexpected error: %v", err)
		}
		if path == "" {
			t.Fatal("fetchZipRetry() returned empty path")
		}
		if got := count.Load(); got != 2 {
			t.Errorf("request count = %d, want 2", got)
		}
	})

	t.Run("checksum mismatch does not retry", func(t *testing.T) {
		t.Parallel()

		c, u, count := countingServer(t, func(_ int64, w http.ResponseWriter) {
			_, _ = w.Write([]byte("wrong bytes"))
		})
		destDir := t.TempDir()
		_, err := c.fetchZipRetry(context.Background(), u, sha256Hex(content), destDir, "p.zip")
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("fetchZipRetry() error = %v, want ErrChecksumMismatch", err)
		}
		if got := count.Load(); got != 1 {
			t.Errorf("request count = %d, want 1 (verification failures never retry)", got)
		}
		assertEmptyDir(t, destDir)
	})

	t.Run("cancellation during backoff short-circuits", func(t *testing.T) {
		t.Parallel()

		// Direct withRetry exercise: the op fails transiently and
		// cancels the context, so the backoff sleep must exit
		// immediately instead of running out the remaining attempts.
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		err := withRetry(ctx, func() error {
			calls++
			cancel()
			return fmt.Errorf("GET blob: %w", &statusError{code: http.StatusServiceUnavailable})
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("withRetry() error = %v, want context.Canceled", err)
		}
		// The last transient error must survive alongside the
		// cancellation.
		var se *statusError
		if !errors.As(err, &se) || se.code != http.StatusServiceUnavailable {
			t.Errorf("withRetry() error = %v, want joined statusError 503", err)
		}
		if calls != 1 {
			t.Errorf("op calls = %d, want 1", calls)
		}
	})
}
