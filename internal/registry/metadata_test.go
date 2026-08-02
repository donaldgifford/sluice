// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/donaldgifford/sluice/internal/config"
)

// metaBody renders a v1 download response, with overrides applied to
// the default self-consistent payload.
func metaBody(t *testing.T, overrides map[string]any) string {
	t.Helper()
	m := map[string]any{
		"download_url":          "/files/terraform-provider-null_3.2.4_linux_amd64.zip",
		"filename":              "terraform-provider-null_3.2.4_linux_amd64.zip",
		"shasums_url":           "/files/terraform-provider-null_3.2.4_SHA256SUMS",
		"shasums_signature_url": "/files/terraform-provider-null_3.2.4_SHA256SUMS.sig",
		"os":                    "linux",
		"arch":                  "amd64",
		"shasum":                strings.Repeat("ab", 32),
		"signing_keys": map[string]any{
			"gpg_keys": []map[string]any{
				{"key_id": "34365D9472D7468F", "ascii_armor": "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...\n-----END PGP PUBLIC KEY BLOCK-----"},
			},
		},
	}
	for k, v := range overrides {
		if v == nil {
			delete(m, k)
			continue
		}
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshaling metadata: %v", err)
	}
	return string(b)
}

// metaServer serves body (or the given status) for any /v1/providers
// download path and returns a Client routed at it.
func metaServer(t *testing.T, status int, body string) (*Client, *httptest.Server) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/providers/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New(WithBaseURL(func(string) string { return srv.URL })), srv
}

var linuxAmd64 = config.Platform{OS: "linux", Arch: "amd64"}

func TestDownloadMeta(t *testing.T) {
	t.Parallel()

	t.Run("relative URLs resolve against the request URL", func(t *testing.T) {
		t.Parallel()

		c, srv := metaServer(t, http.StatusOK, metaBody(t, nil))
		meta, err := c.downloadMeta(context.Background(), providerAddr{"registry.terraform.io", "hashicorp", "null"}, "3.2.4", linuxAmd64)
		if err != nil {
			t.Fatalf("downloadMeta() unexpected error: %v", err)
		}
		wantZip := srv.URL + "/files/terraform-provider-null_3.2.4_linux_amd64.zip"
		if meta.DownloadURL != wantZip {
			t.Errorf("DownloadURL = %q, want %q", meta.DownloadURL, wantZip)
		}
		if !strings.HasPrefix(meta.ShasumsURL, srv.URL+"/files/") ||
			!strings.HasPrefix(meta.ShasumsSignatureURL, srv.URL+"/files/") {
			t.Errorf("sums URLs not resolved: %q, %q", meta.ShasumsURL, meta.ShasumsSignatureURL)
		}
		if meta.Filename != "terraform-provider-null_3.2.4_linux_amd64.zip" {
			t.Errorf("Filename = %q", meta.Filename)
		}
		if len(meta.SigningKeys.GPGKeys) != 1 || meta.SigningKeys.GPGKeys[0].KeyID != "34365D9472D7468F" {
			t.Errorf("SigningKeys = %+v", meta.SigningKeys)
		}
	})

	t.Run("absolute cross-host URLs pass through untouched", func(t *testing.T) {
		t.Parallel()

		abs := "https://releases.example.com/null/3.2.4/terraform-provider-null_3.2.4_linux_amd64.zip"
		c, _ := metaServer(t, http.StatusOK, metaBody(t, map[string]any{"download_url": abs}))
		meta, err := c.downloadMeta(context.Background(), providerAddr{"registry.terraform.io", "hashicorp", "null"}, "3.2.4", linuxAmd64)
		if err != nil {
			t.Fatalf("downloadMeta() unexpected error: %v", err)
		}
		if meta.DownloadURL != abs {
			t.Errorf("DownloadURL = %q, want %q", meta.DownloadURL, abs)
		}
	})

	t.Run("non-200 status is an error carrying the code", func(t *testing.T) {
		t.Parallel()

		c, _ := metaServer(t, http.StatusNotFound, "not found")
		_, err := c.downloadMeta(context.Background(), providerAddr{"registry.terraform.io", "hashicorp", "null"}, "9.9.9", linuxAmd64)
		var se *statusError
		if !errors.As(err, &se) || se.code != http.StatusNotFound {
			t.Fatalf("downloadMeta() error = %v, want statusError 404", err)
		}
	})

	t.Run("oversized response is rejected", func(t *testing.T) {
		t.Parallel()

		c, _ := metaServer(t, http.StatusOK, strings.Repeat("x", maxDocBytes+10))
		_, err := c.downloadMeta(context.Background(), providerAddr{"registry.terraform.io", "hashicorp", "null"}, "3.2.4", linuxAmd64)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("downloadMeta() error = %v, want size-bound rejection", err)
		}
	})

	t.Run("malformed JSON is an error", func(t *testing.T) {
		t.Parallel()

		c, _ := metaServer(t, http.StatusOK, "{not json")
		_, err := c.downloadMeta(context.Background(), providerAddr{"registry.terraform.io", "hashicorp", "null"}, "3.2.4", linuxAmd64)
		if err == nil || !strings.Contains(err.Error(), "decoding download metadata") {
			t.Fatalf("downloadMeta() error = %v, want decode failure", err)
		}
	})
}

func TestDownloadMetaValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		overrides map[string]any
		wantMsg   string
	}{
		{"missing download_url", map[string]any{"download_url": nil}, "missing download_url"},
		{"missing filename", map[string]any{"filename": nil}, "missing filename"},
		{"missing shasums_url", map[string]any{"shasums_url": nil}, "missing shasums_url"},
		{"missing shasums_signature_url", map[string]any{"shasums_signature_url": nil}, "missing shasums_signature_url"},
		{"os mismatch", map[string]any{"os": "windows"}, "requested linux_amd64"},
		{"arch mismatch", map[string]any{"arch": "arm64"}, "requested linux_amd64"},
		{
			// A registry naming its zip after some other artifact
			// could steer which basename gets staged (and later,
			// which mirror object gets written).
			"filename not matching the tuple",
			map[string]any{"filename": "terraform-provider-aws_6.0.0_linux_amd64.zip"},
			"does not match expected",
		},
		{"filename with slash", map[string]any{"filename": "../../etc/evil.zip"}, "not a bare file name"},
		{"filename with backslash", map[string]any{"filename": `..\evil.zip`}, "not a bare file name"},
		{"filename dot dot", map[string]any{"filename": ".."}, "not a bare file name"},
		{"no signing keys", map[string]any{"signing_keys": map[string]any{"gpg_keys": []map[string]any{}}}, "no signing keys"},
		{
			"empty ascii_armor",
			map[string]any{"signing_keys": map[string]any{"gpg_keys": []map[string]any{{"key_id": "AA", "ascii_armor": "  "}}}},
			"empty ascii_armor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := metaServer(t, http.StatusOK, metaBody(t, tt.overrides))
			_, err := c.downloadMeta(context.Background(), providerAddr{"registry.terraform.io", "hashicorp", "null"}, "3.2.4", linuxAmd64)
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("downloadMeta() error = %v, want containing %q", err, tt.wantMsg)
			}
		})
	}
}

func TestResolveMetaURLsRejectsHTTPInProduction(t *testing.T) {
	t.Parallel()

	// Production mode: no WithBaseURL. Direct call — no network.
	c := New()
	reqURL, _ := url.Parse("https://registry.terraform.io/v1/providers/hashicorp/null/3.2.4/download/linux/amd64")
	meta := &downloadMetadata{
		DownloadURL:         "http://releases.example.com/plain.zip",
		ShasumsURL:          "https://ok.example.com/SHA256SUMS",
		ShasumsSignatureURL: "https://ok.example.com/SHA256SUMS.sig",
	}
	err := c.resolveMetaURLs(reqURL, meta)
	if err == nil || !strings.Contains(err.Error(), "refusing non-https URL") {
		t.Fatalf("resolveMetaURLs() error = %v, want https refusal", err)
	}
}

func TestCheckURLAllowsHTTPOnlyInTestMode(t *testing.T) {
	t.Parallel()

	httpURL, _ := url.Parse("http://127.0.0.1:1/x")
	httpsURL, _ := url.Parse("https://registry.terraform.io/x")

	prod := New()
	if err := prod.checkURL(httpsURL); err != nil {
		t.Errorf("production https: unexpected error %v", err)
	}
	if err := prod.checkURL(httpURL); err == nil {
		t.Error("production http: error = nil, want refusal")
	}

	test := New(WithBaseURL(func(string) string { return "http://127.0.0.1:1" }))
	if err := test.checkURL(httpURL); err != nil {
		t.Errorf("test-mode http: unexpected error %v", err)
	}
}
