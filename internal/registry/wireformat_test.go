// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/donaldgifford/sluice/internal/config"
)

// TestRealRegistryWireFormat decodes responses recorded verbatim from
// both live registries. Hand-written fixtures can agree with a wrong
// struct tag — that is exactly how the signing-keys field was decoded
// as gpg_keys and silently came back empty against the real API. These
// goldens are the only test that pins the wire contract, so update
// them by re-recording, never by hand.
func TestRealRegistryWireFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		file      string
		wantKeyID string
		wantSum   string
	}{
		{
			name:      "registry.terraform.io",
			file:      "terraform-io-download.json",
			wantKeyID: "34365D9472D7468F",
			wantSum:   "9d32ac3619cfc93eb3c4f423492a8e0f79db05fec58e449dee9b2d5873d5f69f",
		},
		{
			name:      "registry.opentofu.org",
			file:      "opentofu-org-download.json",
			wantKeyID: "0C0AF313E5FD9F80",
			wantSum:   "3d106c7e32a929e2843f732625a582e562ff09120021e510a51a6f5d01175b8d",
		},
	}

	addr := providerAddr{hostname: "registry.terraform.io", namespace: "hashicorp", typ: "null"}
	platform := config.Platform{OS: "linux", Arch: "amd64"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, err := os.ReadFile(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatalf("reading golden: %v", err)
			}
			var meta downloadMetadata
			if err := json.Unmarshal(body, &meta); err != nil {
				t.Fatalf("decoding golden: %v", err)
			}

			// The whole verification chain hangs off these fields; a
			// renamed or missing one must fail here, not in production.
			if err := validateMeta(&meta, addr, "3.2.4", platform); err != nil {
				t.Fatalf("validateMeta on a real response: %v", err)
			}
			if len(meta.SigningKeys.GPGKeys) != 1 {
				t.Fatalf("gpg keys = %d, want 1", len(meta.SigningKeys.GPGKeys))
			}
			if got := meta.SigningKeys.GPGKeys[0].KeyID; got != tt.wantKeyID {
				t.Errorf("key_id = %q, want %q", got, tt.wantKeyID)
			}
			if meta.Shasum != tt.wantSum {
				t.Errorf("shasum = %q, want %q", meta.Shasum, tt.wantSum)
			}
			// The armored key must actually parse into a keyring.
			if _, err := assembleKeyring(meta.SigningKeys.GPGKeys, nil); err != nil {
				t.Errorf("assembling keyring from the published key: %v", err)
			}
		})
	}
}
