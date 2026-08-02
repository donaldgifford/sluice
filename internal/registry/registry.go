// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/hash"
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

	// defaultDocTimeout caps metadata, sums, and signature fetches.
	// Zip downloads deliberately get no fixed timeout — a large
	// archive on a slow link is legitimate — only the caller's
	// context, the response-header timeout, and the size bound limit
	// them.
	defaultDocTimeout = 30 * time.Second

	// maxRedirects bounds redirect chains once our CheckRedirect
	// replaces net/http's implicit default.
	maxRedirects = 10
)

// Client fetches and verifies provider release artifacts from origin
// registries. The zero value is not usable; construct with [New].
type Client struct {
	httpClient  *http.Client
	baseURL     func(hostname string) string
	maxZipBytes int64
	docTimeout  time.Duration
}

// Option configures a [Client].
type Option func(*Client)

// WithHTTPClient replaces the underlying HTTP client. The client's
// CheckRedirect is overwritten by the package's https-enforcing
// redirect policy — the transport floor is not injectable.
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

// New returns a Client with sane production defaults: https-only
// (enforced on every redirect hop, not just the starting URL), 2 GiB
// zip bound, 30s timeout on document fetches and response headers.
// No overall timeout is set on the HTTP client itself — zip streams
// are bounded by size and context instead.
func New(opts ...Option) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:errcheck // DefaultTransport is *http.Transport by definition
	// Bounds the wait for headers on every request — including the
	// zip GET, whose body time is deliberately unbounded — so a
	// hostile host that accepts and never responds cannot hang a
	// deadline-free caller forever.
	transport.ResponseHeaderTimeout = defaultDocTimeout

	c := &Client{
		httpClient:  &http.Client{Transport: transport},
		maxZipBytes: defaultMaxZipBytes,
		docTimeout:  defaultDocTimeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	// Installed after options on purpose: the https floor applies to
	// every hop of every request, injected client or not.
	c.httpClient.CheckRedirect = c.redirectPolicy
	return c
}

// redirectPolicy enforces the transport floor across redirect hops.
// net/http happily follows https→http downgrades by default, which
// would let a single 302 route the trust-root metadata fetch — keys,
// sums, sig — through plaintext unchecked.
func (c *Client) redirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	return c.checkURL(req.URL)
}

// Artifact is a fully verified, staged provider release archive —
// everything Phase 4's publisher needs for upload, the version
// document, the audit line, and the attestation, with no crypto facts
// re-derived downstream.
type Artifact struct {
	Source   string // "hostname/namespace/type"
	Version  string
	Platform config.Platform
	Path     string // staged zip: destDir/<Filename>
	Filename string // upstream release filename == mirror object name
	SHA256   string // hex, from the signed sums entry (== streamed hash)
	H1       string // "h1:..." dirhash of the zip contents
	// SigningKeyID is the upper-hex primary key ID of the
	// registry-published key that verified SHA256SUMS.
	SigningKeyID string
}

// FetchVerified runs the full verification chain for one (provider,
// version, platform) tuple: metadata → GPG-verified sums → streamed
// zip download with SHA-256 → "h1:" dirhash — and renames the zip
// into destDir only after every check passes. On any error nothing is
// staged. destDir must exist and should be private (0700): the
// verify-then-stage handoff assumes nothing else writes to it. The
// temp file lives inside it so the final rename is atomic.
func (c *Client) FetchVerified(ctx context.Context, source, version string, platform config.Platform, destDir string) (*Artifact, error) {
	pstr := platform.String()
	addr, err := parseSource(source)
	if err != nil {
		return nil, &Error{Source: source, Version: version, Platform: pstr, Step: "metadata", Err: err}
	}

	meta, err := c.downloadMeta(ctx, addr, version, platform)
	if err != nil {
		return nil, fail(addr, version, pstr, "metadata", err)
	}

	sums, err := c.get(ctx, meta.ShasumsURL)
	if err != nil {
		return nil, fail(addr, version, pstr, "sums", fmt.Errorf("fetching SHA256SUMS: %w", err))
	}
	sig, err := c.get(ctx, meta.ShasumsSignatureURL)
	if err != nil {
		return nil, fail(addr, version, pstr, "signature", fmt.Errorf("fetching SHA256SUMS.sig: %w", err))
	}

	keyID, err := verifySums(meta.SigningKeys.GPGKeys, sums, sig)
	if err != nil {
		return nil, fail(addr, version, pstr, "signature", err)
	}

	wantHex, err := sumsEntry(sums, meta.Filename)
	if err != nil {
		return nil, fail(addr, version, pstr, "sums", err)
	}
	// The signed sums are the trust root, but a registry whose
	// metadata shasum contradicts them is an incident to surface,
	// not to paper over.
	if meta.Shasum != "" && meta.Shasum != wantHex {
		return nil, fail(addr, version, pstr, "sums",
			fmt.Errorf("metadata shasum %s contradicts signed sums entry %s", meta.Shasum, wantHex))
	}

	tempPath, err := c.fetchZipRetry(ctx, meta.DownloadURL, wantHex, destDir, meta.Filename)
	if err != nil {
		return nil, fail(addr, version, pstr, "download", err)
	}

	h1, err := hash.Zip(tempPath)
	if err != nil {
		if rerr := os.Remove(tempPath); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return nil, fail(addr, version, pstr, "hash", err)
	}

	finalPath := filepath.Join(destDir, meta.Filename)
	if err := stageZip(tempPath, finalPath); err != nil {
		return nil, fail(addr, version, pstr, "download", err)
	}

	return &Artifact{
		Source:       addr.String(),
		Version:      version,
		Platform:     platform,
		Path:         finalPath,
		Filename:     meta.Filename,
		SHA256:       wantHex,
		H1:           h1,
		SigningKeyID: keyID,
	}, nil
}

// stageZip is the commit point: only a fully verified zip ever
// exists under its final name — and never on top of something
// already staged. A registry choosing another artifact's filename
// must not be able to replace verified bytes in a shared staging
// directory. On failure the temp file is removed.
func stageZip(tempPath, finalPath string) error {
	var err error
	if _, lerr := os.Lstat(finalPath); lerr == nil {
		err = fmt.Errorf("refusing to overwrite already-staged %s", finalPath)
	} else if rerr := os.Rename(tempPath, finalPath); rerr != nil {
		err = fmt.Errorf("staging verified zip: %w", rerr)
	} else {
		return nil
	}
	if rerr := os.Remove(tempPath); rerr != nil {
		err = errors.Join(err, rerr)
	}
	return err
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
	// The hostname is concatenated into the metadata URL, so it must
	// be exactly an authority and nothing more — no credentials, no
	// path, query, or fragment smuggled past a caller that skipped
	// config validation.
	u, err := url.Parse("https://" + parts[0])
	if err != nil || u.Host != parts[0] || u.User != nil {
		return providerAddr{}, fmt.Errorf("malformed registry hostname %q", parts[0])
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

// errInsecureURL classifies transport-floor refusals so the retry
// layer treats them as permanent — repeating a policy violation
// cannot heal it.
var errInsecureURL = errors.New("refusing non-https URL")

// checkURL enforces the transport floor on every fetched URL: https
// always, plain http only under the WithBaseURL test seam.
func (c *Client) checkURL(u *url.URL) error {
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && c.baseURL != nil:
		return nil
	default:
		return fmt.Errorf("%w %q", errInsecureURL, u)
	}
}
