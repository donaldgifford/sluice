// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/sluice/internal/hash"
)

func TestParseSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		want    providerAddr
		wantErr bool
	}{
		{
			name:   "terraform registry",
			source: "registry.terraform.io/hashicorp/aws",
			want:   providerAddr{"registry.terraform.io", "hashicorp", "aws"},
		},
		{
			name:   "opentofu registry",
			source: "registry.opentofu.org/hashicorp/null",
			want:   providerAddr{"registry.opentofu.org", "hashicorp", "null"},
		},
		{name: "two segments", source: "hashicorp/aws", wantErr: true},
		{name: "four segments", source: "reg.example.com/a/b/c", wantErr: true},
		{name: "empty segment", source: "reg.example.com//aws", wantErr: true},
		{name: "empty", source: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseSource(tt.source)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSource(%q) error = nil, want non-nil", tt.source)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSource(%q) unexpected error: %v", tt.source, err)
			}
			if got != tt.want {
				t.Errorf("parseSource(%q) = %+v, want %+v", tt.source, got, tt.want)
			}
			if got.String() != tt.source {
				t.Errorf("String() = %q, want round-trip to %q", got.String(), tt.source)
			}
		})
	}
}

// assertArtifact checks the staged file and every field Phase 4
// consumes.
func assertArtifact(t *testing.T, f *fakeRegistry, a *Artifact, destDir string) {
	t.Helper()

	if a.Source != f.source || a.Version != f.version || a.Platform != f.plat {
		t.Errorf("tuple = (%s, %s, %s), want (%s, %s, %s)",
			a.Source, a.Version, a.Platform, f.source, f.version, f.plat)
	}
	if a.Filename != f.zipName {
		t.Errorf("Filename = %q, want %q", a.Filename, f.zipName)
	}
	wantPath := filepath.Join(destDir, f.zipName)
	if a.Path != wantPath {
		t.Errorf("Path = %q, want %q", a.Path, wantPath)
	}
	staged, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatalf("reading staged zip: %v", err)
	}
	if sha256Hex(staged) != a.SHA256 || a.SHA256 != sha256Hex(f.zip) {
		t.Errorf("SHA256 = %s, want %s", a.SHA256, sha256Hex(f.zip))
	}
	wantH1, err := hash.Zip(a.Path)
	if err != nil {
		t.Fatalf("recomputing h1: %v", err)
	}
	if a.H1 != wantH1 || !strings.HasPrefix(a.H1, "h1:") {
		t.Errorf("H1 = %q, want %q", a.H1, wantH1)
	}
	if want := f.key.PrimaryKey.KeyIdString(); a.SigningKeyID != want {
		t.Errorf("SigningKeyID = %q, want %q", a.SigningKeyID, want)
	}
	// Exactly the verified artifact, no temp orphans.
	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatalf("reading destDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != f.zipName {
		t.Errorf("destDir contents = %v, want exactly [%s]", entries, f.zipName)
	}
}

func TestFetchVerified(t *testing.T) {
	t.Parallel()

	files := map[string]string{"terraform-provider-null_v3.2.4_x5": "fake binary\n"}

	t.Run("terraform-shaped registry with relative URLs", func(t *testing.T) {
		t.Parallel()

		f := newFakeRegistry(t, "registry.terraform.io/hashicorp/null", "3.2.4", linuxAmd64, files)
		destDir := t.TempDir()
		a, err := f.client.FetchVerified(
			context.Background(),
			&FetchRequest{Source: f.source, Version: f.version, Platform: f.plat, DestDir: destDir},
		)
		if err != nil {
			t.Fatalf("FetchVerified() unexpected error: %v", err)
		}
		assertArtifact(t, f, a, destDir)
	})

	t.Run("opentofu-shaped registry with cross-host absolute zip URL", func(t *testing.T) {
		t.Parallel()

		// The OpenTofu registry serves metadata itself but hosts zips
		// on GitHub releases — a different host entirely. Model that
		// with a second server for the zip; trust comes from the
		// signed sums, never from transport origin.
		f := newFakeRegistry(t, "registry.opentofu.org/hashicorp/null", "3.3.0", linuxAmd64, files)
		cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(f.zip)
		}))
		t.Cleanup(cdn.Close)
		f.absZipURL = cdn.URL + "/github/releases/download/v3.3.0/" + f.zipName

		destDir := t.TempDir()
		a, err := f.client.FetchVerified(
			context.Background(),
			&FetchRequest{Source: f.source, Version: f.version, Platform: f.plat, DestDir: destDir},
		)
		if err != nil {
			t.Fatalf("FetchVerified() unexpected error: %v", err)
		}
		assertArtifact(t, f, a, destDir)
	})

	t.Run("multi-platform sums document", func(t *testing.T) {
		t.Parallel()

		f := newFakeRegistry(t, "registry.terraform.io/hashicorp/null", "3.2.4", linuxAmd64, files)
		otherLine := strings.Repeat("cd", 32) + "  terraform-provider-null_3.2.4_darwin_arm64.zip\n"
		f.sums = append([]byte(otherLine), f.sums...)
		f.resign(t)

		destDir := t.TempDir()
		a, err := f.client.FetchVerified(
			context.Background(),
			&FetchRequest{Source: f.source, Version: f.version, Platform: f.plat, DestDir: destDir},
		)
		if err != nil {
			t.Fatalf("FetchVerified() unexpected error: %v", err)
		}
		assertArtifact(t, f, a, destDir)
	})

	t.Run("signature by the second of N published keys", func(t *testing.T) {
		t.Parallel()

		f := newFakeRegistry(t, "registry.terraform.io/hashicorp/null", "3.2.4", linuxAmd64, files)
		older := newTestKey(t, "retired")
		f.published = publishedKeys(t, older, f.key) // signer is key 2 of 2

		destDir := t.TempDir()
		a, err := f.client.FetchVerified(
			context.Background(),
			&FetchRequest{Source: f.source, Version: f.version, Platform: f.plat, DestDir: destDir},
		)
		if err != nil {
			t.Fatalf("FetchVerified() unexpected error: %v", err)
		}
		assertArtifact(t, f, a, destDir)
	})

	t.Run("metadata shasum contradicting signed sums fails", func(t *testing.T) {
		t.Parallel()

		f := newFakeRegistry(t, "registry.terraform.io/hashicorp/null", "3.2.4", linuxAmd64, files)
		f.shasum = strings.Repeat("ef", 32)

		destDir := t.TempDir()
		_, err := f.client.FetchVerified(
			context.Background(),
			&FetchRequest{Source: f.source, Version: f.version, Platform: f.plat, DestDir: destDir},
		)
		if err == nil || !strings.Contains(err.Error(), "contradicts signed sums entry") {
			t.Fatalf("FetchVerified() error = %v, want self-consistency failure", err)
		}
		assertEmptyDir(t, destDir)
	})

	t.Run("malformed source wraps into Error", func(t *testing.T) {
		t.Parallel()

		c := New()
		_, err := c.FetchVerified(
			context.Background(),
			&FetchRequest{Source: "not-an-address", Version: "1.0.0", Platform: linuxAmd64, DestDir: t.TempDir()},
		)
		var re *Error
		if !errors.As(err, &re) || re.Source != "not-an-address" || re.Step != "metadata" {
			t.Fatalf("FetchVerified() error = %v, want *Error with step metadata", err)
		}
	})
}

func TestRedirectPolicy(t *testing.T) {
	t.Parallel()

	t.Run("refuses https to http downgrade in production", func(t *testing.T) {
		t.Parallel()

		c := New()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://mitm.example.com/zip", http.NoBody)
		err := c.httpClient.CheckRedirect(req, []*http.Request{{}})
		if !errors.Is(err, errInsecureURL) {
			t.Fatalf("CheckRedirect() error = %v, want errInsecureURL", err)
		}
	})

	t.Run("allows https hops in production", func(t *testing.T) {
		t.Parallel()

		c := New()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://cdn.example.com/zip", http.NoBody)
		if err := c.httpClient.CheckRedirect(req, []*http.Request{{}}); err != nil {
			t.Fatalf("CheckRedirect() unexpected error: %v", err)
		}
	})

	t.Run("bounds the redirect chain", func(t *testing.T) {
		t.Parallel()

		c := New()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://cdn.example.com/zip", http.NoBody)
		via := make([]*http.Request, maxRedirects)
		err := c.httpClient.CheckRedirect(req, via)
		if err == nil || !strings.Contains(err.Error(), "stopped after") {
			t.Fatalf("CheckRedirect() error = %v, want redirect-limit refusal", err)
		}
	})

	t.Run("follows floor-satisfying redirects end to end", func(t *testing.T) {
		t.Parallel()

		// A registry that 302s its zip to a second path — allowed as
		// long as every hop satisfies the transport floor.
		content := []byte("redirected zip bytes")
		mux := http.NewServeMux()
		mux.HandleFunc("GET /zip", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/real-zip", http.StatusFound)
		})
		mux.HandleFunc("GET /real-zip", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(content)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		c := New(WithBaseURL(func(string) string { return srv.URL }))

		destDir := t.TempDir()
		path, err := c.fetchZip(context.Background(), srv.URL+"/zip", sha256Hex(content), destDir, "p.zip")
		if err != nil {
			t.Fatalf("fetchZip() unexpected error: %v", err)
		}
		if path == "" {
			t.Fatal("fetchZip() returned empty path")
		}
	})
}

func TestWithHTTPClientKeepsRedirectFloor(t *testing.T) {
	t.Parallel()

	// An injected client must not carry its own redirect policy past
	// the transport floor — New overwrites CheckRedirect regardless.
	custom := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	c := New(WithHTTPClient(custom))
	if c.httpClient != custom {
		t.Fatal("WithHTTPClient did not install the injected client")
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://mitm.example.com/zip", http.NoBody)
	if err := c.httpClient.CheckRedirect(req, nil); !errors.Is(err, errInsecureURL) {
		t.Fatalf("CheckRedirect() error = %v, want errInsecureURL on injected client", err)
	}
}

func TestFetchVerifiedRefusesToClobberStagedFile(t *testing.T) {
	t.Parallel()

	files := map[string]string{"terraform-provider-null_v3.2.4_x5": "fake binary\n"}
	f := newFakeRegistry(t, "registry.terraform.io/hashicorp/null", "3.2.4", linuxAmd64, files)

	destDir := t.TempDir()
	staged := filepath.Join(destDir, f.zipName)
	if err := os.WriteFile(staged, []byte("previously verified bytes"), 0o600); err != nil {
		t.Fatalf("pre-staging: %v", err)
	}

	_, err := f.client.FetchVerified(context.Background(), &FetchRequest{Source: f.source, Version: f.version, Platform: f.plat, DestDir: destDir})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("FetchVerified() error = %v, want overwrite refusal", err)
	}
	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("reading pre-staged file: %v", err)
	}
	if string(got) != "previously verified bytes" {
		t.Errorf("pre-staged file was modified: %q", got)
	}
	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatalf("reading destDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("destDir has %d entries, want only the pre-staged file (no temp orphans)", len(entries))
	}
}
