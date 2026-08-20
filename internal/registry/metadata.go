// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/donaldgifford/sluice/internal/config"
)

// gpgKey is one registry-published signing key.
type gpgKey struct {
	KeyID      string `json:"key_id"`
	ASCIIArmor string `json:"ascii_armor"`
}

// downloadMetadata is the v1 download response for one (provider,
// version, platform). After downloadMeta returns, the three URL
// fields are absolute (relative URLs are resolved against the request
// URL) and scheme-checked.
type downloadMetadata struct {
	DownloadURL         string `json:"download_url"`
	Filename            string `json:"filename"`
	ShasumsURL          string `json:"shasums_url"`
	ShasumsSignatureURL string `json:"shasums_signature_url"`
	OS                  string `json:"os"`
	Arch                string `json:"arch"`
	Shasum              string `json:"shasum"`
	SigningKeys         struct {
		// Both registry.terraform.io and registry.opentofu.org name
		// this gpg_public_keys — verified against live responses.
		GPGKeys []gpgKey `json:"gpg_public_keys"`
	} `json:"signing_keys"`
}

// downloadMeta resolves download metadata from the origin registry's
// v1 API and validates it fail-closed: every field the verification
// chain depends on must be present and self-consistent.
func (c *Client) downloadMeta(ctx context.Context, addr providerAddr, version string, p config.Platform) (*downloadMetadata, error) {
	reqURL, err := url.Parse(fmt.Sprintf("%s/v1/providers/%s/%s/%s/download/%s/%s",
		c.base(addr),
		url.PathEscape(addr.namespace), url.PathEscape(addr.typ),
		url.PathEscape(version),
		url.PathEscape(p.OS), url.PathEscape(p.Arch)))
	if err != nil {
		return nil, fmt.Errorf("building metadata URL: %w", err)
	}
	if err := c.checkURL(reqURL); err != nil {
		return nil, err
	}

	body, err := c.get(ctx, reqURL.String())
	if err != nil {
		return nil, fmt.Errorf("fetching download metadata: %w", err)
	}

	var meta downloadMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("decoding download metadata: %w", err)
	}
	if err := validateMeta(&meta, addr, version, p); err != nil {
		return nil, fmt.Errorf("invalid download metadata: %w", err)
	}
	if err := c.resolveMetaURLs(reqURL, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// validateMeta rejects any response missing a field the verification
// chain depends on, echoing the wrong platform, or steering the
// filename — which becomes both an on-disk path and the mirror
// object name, the one injection surface a hostile registry has into
// local disk and mirror key layout. Both registries require release
// archives named terraform-provider-<type>_<version>_<os>_<arch>.zip,
// so anything else is refused outright.
func validateMeta(meta *downloadMetadata, addr providerAddr, version string, p config.Platform) error {
	expected := fmt.Sprintf("terraform-provider-%s_%s_%s_%s.zip", addr.typ, version, p.OS, p.Arch)
	switch {
	case meta.DownloadURL == "":
		return errors.New("missing download_url")
	case meta.Filename == "":
		return errors.New("missing filename")
	case meta.ShasumsURL == "":
		return errors.New("missing shasums_url")
	case meta.ShasumsSignatureURL == "":
		return errors.New("missing shasums_signature_url")
	case meta.OS != p.OS || meta.Arch != p.Arch:
		return fmt.Errorf("response is for %s_%s, requested %s", meta.OS, meta.Arch, p)
	case !isBareFilename(meta.Filename):
		return fmt.Errorf("filename %q is not a bare file name", meta.Filename)
	case meta.Filename != expected:
		return fmt.Errorf("filename %q does not match expected %q for this tuple", meta.Filename, expected)
	case len(meta.SigningKeys.GPGKeys) == 0:
		return errors.New("no signing keys published")
	}
	for _, k := range meta.SigningKeys.GPGKeys {
		if strings.TrimSpace(k.ASCIIArmor) == "" {
			return fmt.Errorf("signing key %q has empty ascii_armor", k.KeyID)
		}
	}
	return nil
}

// resolveMetaURLs makes the three artifact URLs absolute (registries
// may return them relative to the request URL) and scheme-checks each.
func (c *Client) resolveMetaURLs(reqURL *url.URL, meta *downloadMetadata) error {
	for name, field := range map[string]*string{
		"download_url":          &meta.DownloadURL,
		"shasums_url":           &meta.ShasumsURL,
		"shasums_signature_url": &meta.ShasumsSignatureURL,
	} {
		u, err := url.Parse(*field)
		if err != nil {
			return fmt.Errorf("invalid %s: %w", name, err)
		}
		resolved := reqURL.ResolveReference(u)
		if err := c.checkURL(resolved); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		*field = resolved.String()
	}
	return nil
}

// isBareFilename reports whether name is a plain file name with no
// path structure. Backslash is rejected too: it is a separator on
// Windows and has no business in a release file name anywhere.
func isBareFilename(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// get fetches a small document with retry and returns at most
// maxDocBytes of the body; a larger body or a non-200 status is an
// error. Each HTTP call is one retry unit. An attempt-local timeout
// is reclassified transient — only the caller's own context ending
// is permanent.
func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	var body []byte
	err := withRetry(ctx, func() error {
		b, err := c.getOnce(ctx, u)
		if err != nil {
			if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("%w: %w", errAttemptTimeout, err)
			}
			return err
		}
		body = b
		return nil
	})
	return body, err
}

// getOnce is a single document fetch attempt, carrying its own
// document timeout on top of the caller's context.
func (c *Client) getOnce(ctx context.Context, u string) ([]byte, error) {
	const maxBytes = int64(maxDocBytes)
	ctx, cancel := context.WithTimeout(ctx, c.docTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", u, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %w", u, &statusError{code: resp.StatusCode})
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", u, err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", u, maxBytes)
	}
	return body, nil
}

// statusError carries a non-200 HTTP status so the retry layer can
// classify transient (5xx, 429) from permanent (other 4xx) failures.
type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("unexpected status %d %s", e.code, http.StatusText(e.code))
}
