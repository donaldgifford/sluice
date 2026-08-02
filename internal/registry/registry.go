// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// defaultMaxZipBytes bounds a provider zip download. The registry
	// response carries no trusted size (Content-Length is
	// attacker-controlled alongside the body), so this is a fixed
	// disk/DoS bound — integrity is SHA-256's job. 2 GiB clears every
	// real provider with an order of magnitude of headroom.
	defaultMaxZipBytes = 2 << 30

	// maxDocBytes bounds metadata, SHA256SUMS, and signature
	// responses. A multi-megabyte sums file is anomalous and fails.
	maxDocBytes = 1 << 20

	// docTimeout caps metadata, sums, and signature fetches. Zip
	// downloads deliberately get no fixed timeout — a large archive
	// on a slow link is legitimate — only the caller's context and
	// the size bound limit them.
	docTimeout = 30 * time.Second
)

// Client fetches and verifies provider release artifacts from origin
// registries. The zero value is not usable; construct with [New].
type Client struct {
	httpClient  *http.Client
	baseURL     func(hostname string) string
	maxZipBytes int64
}

// Option configures a [Client].
type Option func(*Client)

// WithHTTPClient replaces the underlying HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.httpClient = c }
}

// WithBaseURL maps a registry hostname to a base URL — the test seam
// for pointing sources at an httptest server. Setting it also permits
// plain-http URLs, which production clients (baseURL nil) never get.
func WithBaseURL(f func(hostname string) string) Option {
	return func(cl *Client) { cl.baseURL = f }
}

// WithMaxZipBytes overrides the zip size bound — a test seam for the
// bound-exceeded failure path.
func WithMaxZipBytes(n int64) Option {
	return func(cl *Client) { cl.maxZipBytes = n }
}

// New returns a Client with sane production defaults: https-only,
// 2 GiB zip bound, 30s timeout on document fetches. No overall
// timeout is set on the HTTP client itself — zip streams are bounded
// by size and context instead.
func New(opts ...Option) *Client {
	c := &Client{
		httpClient:  &http.Client{},
		maxZipBytes: defaultMaxZipBytes,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// fail wraps err into the package's tuple-carrying [Error].
func fail(addr providerAddr, version, platform, step string, err error) *Error {
	return &Error{
		Source:   addr.String(),
		Version:  version,
		Platform: platform,
		Step:     step,
		Err:      err,
	}
}

// providerAddr is a parsed provider source address. Config has
// already validated and normalized the source; registry re-parses
// defensively rather than assuming.
type providerAddr struct {
	hostname  string
	namespace string
	typ       string
}

func parseSource(source string) (providerAddr, error) {
	parts := strings.Split(source, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return providerAddr{}, fmt.Errorf("malformed provider source %q: want hostname/namespace/type", source)
	}
	return providerAddr{hostname: parts[0], namespace: parts[1], typ: parts[2]}, nil
}

func (a providerAddr) String() string {
	return a.hostname + "/" + a.namespace + "/" + a.typ
}

// base returns the registry base URL for the address's hostname —
// "https://<hostname>" unless the test seam overrides it.
func (c *Client) base(a providerAddr) string {
	if c.baseURL != nil {
		return c.baseURL(a.hostname)
	}
	return "https://" + a.hostname
}

// checkURL enforces the transport floor on every fetched URL: https
// always, plain http only under the WithBaseURL test seam.
func (c *Client) checkURL(u *url.URL) error {
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && c.baseURL != nil:
		return nil
	default:
		return fmt.Errorf("refusing non-https URL %q", u)
	}
}
