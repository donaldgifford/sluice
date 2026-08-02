// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
)

// fetchZip streams the zip at urlStr into a temp file inside destDir,
// enforcing the size bound and verifying the SHA-256 computed during
// the stream against wantHex — the entry from the GPG-verified sums.
// On success it returns the temp path; the caller renames it into
// place only after the h1 hash also succeeds, so the rename stays the
// single commit point. On any error nothing is left in destDir.
//
// The temp file lives in destDir itself so the eventual rename is
// same-filesystem atomic.
func (c *Client) fetchZip(ctx context.Context, urlStr, wantHex, destDir, filename string) (path string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("building request for %s: %w", urlStr, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", urlStr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %w", urlStr, &statusError{code: resp.StatusCode})
	}

	f, err := os.CreateTemp(destDir, filename+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	// Fail-closed cleanup: on any error, no partial bytes survive in
	// destDir. Cleanup failures join the primary error — they cannot
	// rescue the download, but they must not vanish either.
	defer func() {
		if err == nil {
			return
		}
		if cerr := f.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
			err = errors.Join(err, cerr)
		}
		if rerr := os.Remove(f.Name()); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}()

	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hasher), io.LimitReader(resp.Body, c.maxZipBytes+1))
	if err != nil {
		return "", fmt.Errorf("streaming zip: %w", err)
	}
	if n > c.maxZipBytes {
		return "", fmt.Errorf("zip exceeds the %d byte size bound", c.maxZipBytes)
	}
	if err = f.Close(); err != nil {
		return "", fmt.Errorf("closing temp file: %w", err)
	}

	if got := hex.EncodeToString(hasher.Sum(nil)); got != wantHex {
		err = fmt.Errorf("%w: streamed %s, signed sums say %s", ErrChecksumMismatch, got, wantHex)
		return "", err
	}
	return f.Name(), nil
}
