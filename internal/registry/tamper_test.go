// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"context"
	"errors"
	"testing"
)

// TestTamperedFixtures is the fail-closed proof for the trust
// boundary: four independent tamper modes, each starting from one
// fully consistent release and breaking exactly one link in the
// chain. Every case must (1) match its sentinel via errors.Is,
// (2) surface as *Error carrying the exact (provider, version,
// platform) tuple via errors.As, and (3) stage nothing — the staging
// directory is empty, no artifact, no temp orphans.
func TestTamperedFixtures(t *testing.T) {
	t.Parallel()

	const (
		source  = "registry.terraform.io/hashicorp/null"
		version = "3.2.4"
	)
	files := map[string]string{"terraform-provider-null_v3.2.4_x5": "fake binary\n"}

	tests := []struct {
		name     string
		tamper   func(t *testing.T, f *fakeRegistry)
		wantErr  error  // sentinel via errors.Is, if set
		wantStep string // *Error.Step assertion, if set
	}{
		{
			// SHA256SUMS.sig is a real signature by the published key
			// — but over different content than the served sums.
			name: "signature invalid for the sums file",
			tamper: func(t *testing.T, f *fakeRegistry) {
				t.Helper()
				f.sig = signDetached(t, f.key, []byte("sums for some other release\n"))
			},
			wantErr: ErrSignature,
		},
		{
			// The sums are correctly signed — by a key the registry
			// never published. Membership in the published keyring is
			// the trust check, so an unknown issuer must fail.
			name: "sums signed by a key the registry did not publish",
			tamper: func(t *testing.T, f *fakeRegistry) {
				t.Helper()
				f.sig = signDetached(t, newTestKey(t, "rogue"), f.sums)
			},
			wantErr: ErrSignature,
		},
		{
			// Signature and sums verify; the zip served differs from
			// what was signed by one flipped byte.
			name: "zip modified after signing",
			tamper: func(t *testing.T, f *fakeRegistry) {
				t.Helper()
				f.zip[len(f.zip)/2] ^= 0xFF
			},
			wantErr: ErrChecksumMismatch,
		},
		{
			// A correctly signed sums document that simply has no
			// entry for the zip: nothing vouches for the file, so
			// nothing is staged.
			name: "zip entry missing from the sums file",
			tamper: func(t *testing.T, f *fakeRegistry) {
				t.Helper()
				f.sums = []byte(sha256Hex([]byte("unrelated")) + "  some-other-file.zip\n")
				f.resign(t)
			},
			wantErr: ErrSumsEntryMissing,
		},
		{
			// The registry consistently signs garbage that is not a
			// zip: signature passes, sha256 passes, and the h1 step
			// must still fail closed and clean up its temp file.
			name: "signed bytes that are not a valid zip",
			tamper: func(t *testing.T, f *fakeRegistry) {
				t.Helper()
				f.zip = []byte("consistently signed, but not a zip archive")
				f.shasum = sha256Hex(f.zip)
				f.sums = []byte(f.shasum + "  " + f.zipName + "\n")
				f.resign(t)
			},
			wantStep: "hash",
		},
		{
			// The published key expired before verification time; the
			// signature was made while it was valid. Default go-crypto
			// config must reject it — this pins the nil-config
			// strictness so a future lenient *packet.Config regresses
			// loudly.
			name: "sums signed by an expired published key",
			tamper: func(t *testing.T, f *fakeRegistry) {
				t.Helper()
				expired, sig := newExpiredKeyAndSig(t, f.sums)
				f.published = publishedKeys(t, expired)
				f.sig = sig
			},
			wantErr: ErrSignature,
		},
		{
			// An ASCII-armored signature where the binary .sig shape
			// belongs — fails closed today, pinned as intentional.
			name: "armored signature instead of binary",
			tamper: func(t *testing.T, f *fakeRegistry) {
				t.Helper()
				f.sig = armorSig(t, f.sig)
			},
			wantErr: ErrSignature,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFakeRegistry(t, source, version, linuxAmd64, files)
			tt.tamper(t, f)

			destDir := t.TempDir()
			_, err := f.client.FetchVerified(
				context.Background(),
				&FetchRequest{Source: source, Version: version, Platform: linuxAmd64, DestDir: destDir},
			)

			if err == nil {
				t.Fatal("FetchVerified() error = nil, want failure")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("FetchVerified() error = %v, want %v", err, tt.wantErr)
			}
			var re *Error
			if !errors.As(err, &re) {
				t.Fatalf("FetchVerified() error = %v, want *Error in the chain", err)
			}
			if re.Source != source || re.Version != version || re.Platform != "linux_amd64" {
				t.Errorf("tuple = (%s, %s, %s), want (%s, %s, linux_amd64)",
					re.Source, re.Version, re.Platform, source, version)
			}
			if tt.wantStep != "" && re.Step != tt.wantStep {
				t.Errorf("Step = %q, want %q", re.Step, tt.wantStep)
			}
			assertEmptyDir(t, destDir)
		})
	}
}

// A shasum tamper on the zip must fail even when the tampered zip's
// own hash appears in a re-signed sums file — the metadata shasum
// cross-check catches a registry contradicting itself, and the
// original signed sums catch everything else. This guards the
// composition, not just individual steps.
func TestTamperedZipWithConsistentlyResignedSums(t *testing.T) {
	t.Parallel()

	files := map[string]string{"terraform-provider-null_v3.2.4_x5": "fake binary\n"}
	f := newFakeRegistry(t, "registry.terraform.io/hashicorp/null", "3.2.4", linuxAmd64, files)

	// Attacker with control of blobs AND sums AND sig — but not the
	// published keyring: swap zip, rewrite sums to match, sign with
	// their own key.
	rogue := newTestKey(t, "rogue")
	f.zip = zipArchive(t, map[string]string{"terraform-provider-null_v3.2.4_x5": "backdoored\n"})
	f.sums = []byte(sha256Hex(f.zip) + "  " + f.zipName + "\n")
	f.sig = signDetached(t, rogue, f.sums)

	destDir := t.TempDir()
	_, err := f.client.FetchVerified(context.Background(), &FetchRequest{Source: f.source, Version: f.version, Platform: f.plat, DestDir: destDir})
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("FetchVerified() error = %v, want ErrSignature", err)
	}
	assertEmptyDir(t, destDir)
}
