// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// assertEmptyDir asserts the fail-closed staging contract: after any
// failure, destDir holds nothing — no artifact, no temp orphans.
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading destDir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("destDir not empty after failure: %v", names)
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// blobServer serves body at /zip and returns a test-mode client.
func blobServer(t *testing.T, handler http.HandlerFunc) (*Client, string) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /zip", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := New(WithBaseURL(func(string) string { return srv.URL }))
	return c, srv.URL + "/zip"
}

func TestFetchZip(t *testing.T) {
	t.Parallel()

	content := []byte("verified provider zip bytes")

	t.Run("streams, verifies, and returns the temp path", func(t *testing.T) {
		t.Parallel()

		c, u := blobServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(content)
		})
		destDir := t.TempDir()
		path, err := c.fetchZip(context.Background(), u, sha256Hex(content), destDir, "p.zip")
		if err != nil {
			t.Fatalf("fetchZip() unexpected error: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading staged temp: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Errorf("staged bytes = %q, want %q", got, content)
		}
		if !strings.HasPrefix(path, destDir) {
			t.Errorf("temp path %q not inside destDir %q", path, destDir)
		}
	})

	t.Run("checksum mismatch stages nothing", func(t *testing.T) {
		t.Parallel()

		c, u := blobServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("tampered bytes"))
		})
		destDir := t.TempDir()
		_, err := c.fetchZip(context.Background(), u, sha256Hex(content), destDir, "p.zip")
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("fetchZip() error = %v, want ErrChecksumMismatch", err)
		}
		assertEmptyDir(t, destDir)
	})

	t.Run("size bound exceeded stages nothing", func(t *testing.T) {
		t.Parallel()

		big := make([]byte, 100)
		c, u := blobServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(big)
		})
		WithMaxZipBytes(8)(c)
		destDir := t.TempDir()
		_, err := c.fetchZip(context.Background(), u, sha256Hex(big), destDir, "p.zip")
		if err == nil || !strings.Contains(err.Error(), "size bound") {
			t.Fatalf("fetchZip() error = %v, want size-bound rejection", err)
		}
		assertEmptyDir(t, destDir)
	})

	t.Run("non-200 stages nothing", func(t *testing.T) {
		t.Parallel()

		c, u := blobServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		destDir := t.TempDir()
		_, err := c.fetchZip(context.Background(), u, sha256Hex(content), destDir, "p.zip")
		var se *statusError
		if !errors.As(err, &se) || se.code != http.StatusServiceUnavailable {
			t.Fatalf("fetchZip() error = %v, want statusError 503", err)
		}
		assertEmptyDir(t, destDir)
	})

	t.Run("mid-stream cut stages nothing", func(t *testing.T) {
		t.Parallel()

		c, u := blobServer(t, func(w http.ResponseWriter, _ *http.Request) {
			// Promise more than we send, then bail — the client sees
			// an unexpected EOF mid-body.
			w.Header().Set("Content-Length", "1024")
			_, _ = w.Write(content[:8])
		})
		destDir := t.TempDir()
		_, err := c.fetchZip(context.Background(), u, sha256Hex(content), destDir, "p.zip")
		if err == nil || !strings.Contains(err.Error(), "streaming zip") {
			t.Fatalf("fetchZip() error = %v, want mid-stream failure", err)
		}
		assertEmptyDir(t, destDir)
	})

	t.Run("canceled context stages nothing", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c, u := blobServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(content)
		})
		destDir := t.TempDir()
		_, err := c.fetchZip(ctx, u, sha256Hex(content), destDir, "p.zip")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("fetchZip() error = %v, want context.Canceled", err)
		}
		assertEmptyDir(t, destDir)
	})

	t.Run("unwritable destDir is an error", func(t *testing.T) {
		t.Parallel()

		c, u := blobServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(content)
		})
		destDir := t.TempDir()
		if err := os.Chmod(destDir, 0o500); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(destDir, 0o700) })
		_, err := c.fetchZip(context.Background(), u, sha256Hex(content), destDir, "other.zip")
		if err == nil || !strings.Contains(err.Error(), "creating temp file") {
			t.Fatalf("fetchZip() error = %v, want temp-create failure", err)
		}
	})
}
