// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// The expired-signing-key override is the one place the verification
// chain can be relaxed, so these tests pin exactly how far it goes:
// expiry only, only for the provider that asked, and never past a
// revocation.

func TestExpiredKeyRejectedByDefault(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")
	key, sig := newExpiredKeyAndSig(t, sums)

	_, _, err := verifySums(publishedKeys(t, key), sums, sig, nil, false)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("verifySums() error = %v, want ErrSignature", err)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error %q does not name expiry", err)
	}
}

func TestExpiredKeyAcceptedWithOverride(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")
	key, sig := newExpiredKeyAndSig(t, sums)

	keyID, expired, err := verifySums(publishedKeys(t, key), sums, sig, nil, true)
	if err != nil {
		t.Fatalf("verifySums() with override: %v", err)
	}
	if !expired {
		t.Error("expired = false, want true so the caller can warn")
	}
	if want := key.PrimaryKey.KeyIdString(); keyID != want {
		t.Errorf("keyID = %q, want %q", keyID, want)
	}
}

// The override relaxes expiry and nothing else. Each case would be
// refused with the override off too; the point is that turning it on
// does not start letting them through.
func TestOverrideRelaxesNothingElse(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")

	tests := []struct {
		name string
		// build returns the published keyring and the signature to check.
		build func(t *testing.T) ([]gpgKey, []byte)
		want  string
	}{
		{
			name: "revoked key stays refused even though it is also expired",
			build: func(t *testing.T) ([]gpgKey, []byte) {
				t.Helper()
				key, sig := newExpiredKeyAndSig(t, sums)
				revokeKey(t, key)
				return publishedKeys(t, key), sig
			},
			want: "revoked",
		},
		{
			name: "signature over other content",
			build: func(t *testing.T) ([]gpgKey, []byte) {
				t.Helper()
				key, _ := newExpiredKeyAndSig(t, []byte("something else entirely\n"))
				_, sig := newExpiredKeyAndSig(t, []byte("something else entirely\n"))
				return publishedKeys(t, key), sig
			},
			want: "",
		},
		{
			name: "signature by a key the registry never published",
			build: func(t *testing.T) ([]gpgKey, []byte) {
				t.Helper()
				published, _ := newExpiredKeyAndSig(t, sums)
				_, rogueSig := newExpiredKeyAndSig(t, sums)
				return publishedKeys(t, published), rogueSig
			},
			want: "",
		},
		{
			name: "garbage signature bytes",
			build: func(t *testing.T) ([]gpgKey, []byte) {
				t.Helper()
				key, _ := newExpiredKeyAndSig(t, sums)
				return publishedKeys(t, key), []byte("not a signature")
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keys, sig := tt.build(t)
			_, _, err := verifySums(keys, sums, sig, nil, true)
			if !errors.Is(err, ErrSignature) {
				t.Fatalf("verifySums() with override = %v, want ErrSignature", err)
			}
			if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

// A signature claiming it was made in the future must not wind the
// clock forward on the signer's say-so. Exercised directly against
// verifyAtSigningTime: the guard runs before signature verification,
// and a key cannot be both expired now and valid tomorrow, so this
// shape is unreachable through verifySums by construction.
func TestVerifyAtSigningTimeRefusesFutureDatedSignature(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")

	key := newTestKey(t, "future")
	var buf bytes.Buffer
	future := &packet.Config{
		Algorithm: packet.PubKeyAlgoEdDSA,
		Time:      func() time.Time { return time.Now().Add(24 * time.Hour) },
	}
	if err := openpgp.DetachSign(&buf, key, bytes.NewReader(sums), future); err != nil {
		t.Fatalf("signing: %v", err)
	}

	keyring, err := assembleKeyring(publishedKeys(t, key), nil)
	if err != nil {
		t.Fatalf("assembling keyring: %v", err)
	}
	_, err = verifyAtSigningTime(keyring, sums, buf.Bytes())
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("verifyAtSigningTime() = %v, want ErrSignature", err)
	}
	if !strings.Contains(err.Error(), "future") {
		t.Errorf("error %q does not explain the future-dated signature", err)
	}
}

// FetchVerified is where the policy actually lands: the same fixture
// fails or succeeds purely on the request's flag.
func TestFetchVerifiedHonorsExpiredKeyPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		allow   bool
		wantErr bool
	}{
		{name: "strict by default", allow: false, wantErr: true},
		{name: "opted in", allow: true, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFakeRegistry(t, "registry.terraform.io/hashicorp/null", "3.2.4", linuxAmd64,
				map[string]string{"terraform-provider-null_v3.2.4_x5": "fake binary\n"})
			// Same fixture the tamper suite uses for expiry: the
			// signature was made while the published key was valid.
			expired, sig := newExpiredKeyAndSig(t, f.sums)
			f.published = publishedKeys(t, expired)
			f.sig = sig
			destDir := t.TempDir()

			art, err := f.client.FetchVerified(t.Context(), &FetchRequest{
				Source: f.source, Version: f.version, Platform: f.plat,
				DestDir: destDir, AllowExpiredSigningKey: tt.allow,
			})
			if tt.wantErr {
				if !errors.Is(err, ErrSignature) {
					t.Fatalf("FetchVerified() error = %v, want ErrSignature", err)
				}
				assertEmptyDir(t, destDir)
				return
			}
			if err != nil {
				t.Fatalf("FetchVerified() unexpected error: %v", err)
			}
			if !art.SigningKeyExpired {
				t.Error("Artifact.SigningKeyExpired = false, want true")
			}
		})
	}
}

// Refreshing a key is the strict fix for the stale-export problem:
// nothing is relaxed, the signature just verifies against the key
// material the owner actually published.
func TestRefreshedKeyReplacesStaleRegistryCopy(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")

	// Two exports of one key: the registry's, which expired an hour
	// ago, and a refreshed one carrying a later self-signature. Same
	// primary fingerprint, exactly as an extension works upstream.
	stale, sig := newExpiredKeyAndSig(t, sums)
	extended := extendKey(t, stale)

	// Without the refresh, strict verification refuses it.
	if _, _, err := verifySums(publishedKeys(t, stale), sums, sig, nil, false); !errors.Is(err, ErrSignature) {
		t.Fatalf("stale copy alone = %v, want ErrSignature", err)
	}

	keyID, expired, err := verifySums(publishedKeys(t, stale), sums, sig, []string{armorPub(t, extended)}, false)
	if err != nil {
		t.Fatalf("verifySums() with the refreshed key: %v", err)
	}
	if expired {
		t.Error("expired = true; a refreshed key verifies strictly, nothing was overridden")
	}
	if want := stale.PrimaryKey.KeyIdString(); keyID != want {
		t.Errorf("keyID = %q, want %q", keyID, want)
	}
}

// The refresh matches on primary fingerprint, so it can only ever
// update a key the registry already published — never add one. A
// rogue export never enters the keyring, so its signature is from an
// unknown entity no matter that it was handed to us as a "refresh".
func TestRefreshedKeyCannotAddTrust(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")

	published := newTestKey(t, "published")
	rogue := newTestKey(t, "rogue")

	_, _, err := verifySums(publishedKeys(t, published), sums,
		signDetached(t, rogue, sums), []string{armorPub(t, rogue)}, true)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("verifySums() = %v, want ErrSignature for an unpublished signer", err)
	}
	if !strings.Contains(err.Error(), "unknown entity") {
		t.Errorf("error %q does not say the signer is unknown", err)
	}
}

// A mirror-wide signing_key_files list is applied to every fetch, so
// an export for one registry's key routinely matches nothing while
// fetching from another. That must stay a no-op, not an error.
func TestRefreshedKeyForAnotherRegistryIsANoOp(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")

	published := newTestKey(t, "published")
	elsewhere := newTestKey(t, "other-registry")

	keyID, _, err := verifySums(publishedKeys(t, published), sums,
		signDetached(t, published, sums), []string{armorPub(t, elsewhere)}, false)
	if err != nil {
		t.Fatalf("verifySums() with an unrelated refresh: %v", err)
	}
	if want := published.PrimaryKey.KeyIdString(); keyID != want {
		t.Errorf("keyID = %q, want %q", keyID, want)
	}
}

// A refresh may only ever add validity. An operator export predating a
// revocation the registry now publishes would otherwise un-revoke a
// compromised key, which is the one thing revocation exists to stop.
func TestRefreshedKeyCannotDropARevocation(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")

	key := newTestKey(t, "release")
	sig := signDetached(t, key, sums)
	preRevocation := copyEntity(t, key) // the operator's stale export
	revokeKey(t, key)                   // ...and then the key is revoked upstream

	// Baseline: the registry's revoked copy alone is refused.
	if _, _, err := verifySums(publishedKeys(t, key), sums, sig, nil, false); !errors.Is(err, ErrSignature) {
		t.Fatalf("revoked key alone = %v, want ErrSignature", err)
	}

	// Offering the pre-revocation export must not resurrect it.
	_, _, err := verifySums(publishedKeys(t, key), sums, sig,
		[]string{armorPub(t, preRevocation)}, false)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("verifySums() = %v, want ErrSignature — the refresh dropped a revocation", err)
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("error %q does not explain that the published key is revoked", err)
	}
}

func TestRefreshedKeyGarbageIsAHardError(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")
	key := newTestKey(t, "release")

	// Silently ignoring it would leave verification running on the
	// stale copy the operator meant to replace.
	_, _, err := verifySums(publishedKeys(t, key), sums, signDetached(t, key, sums),
		[]string{"not armor at all"}, false)
	if err == nil || !strings.Contains(err.Error(), "refreshed signing key") {
		t.Fatalf("verifySums() error = %v, want a refreshed-key parse failure", err)
	}
}

// TestHashiCorpKeyIsExtendedUpstream documents the live condition this
// mechanism exists for, using the two real exports side by side: the
// registry embeds a pre-February 2026 copy that expired 2026-04-18,
// while hashicorp.com publishes the same fingerprint extended to 2030.
func TestHashiCorpKeyIsExtendedUpstream(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join("testdata", "terraform-io-download.json"))
	if err != nil {
		t.Fatalf("reading registry golden: %v", err)
	}
	var meta downloadMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("decoding registry golden: %v", err)
	}
	stale, err := assembleKeyring(meta.SigningKeys.GPGKeys, nil)
	if err != nil {
		t.Fatalf("assembling the registry keyring: %v", err)
	}

	extendedArmor, err := os.ReadFile(filepath.Join("testdata", "hashicorp-extended-key.asc"))
	if err != nil {
		t.Fatalf("reading the extended export: %v", err)
	}
	refreshed, err := assembleKeyring(meta.SigningKeys.GPGKeys, []string{string(extendedArmor)})
	if err != nil {
		t.Fatalf("assembling the refreshed keyring: %v", err)
	}

	// Same key: the refresh replaced metadata, not identity.
	if !bytes.Equal(stale[0].PrimaryKey.Fingerprint, refreshed[0].PrimaryKey.Fingerprint) {
		t.Fatal("refresh changed the primary fingerprint; it must only ever update the same key")
	}
	staleSelf, _ := stale[0].PrimarySelfSignature()
	freshSelf, _ := refreshed[0].PrimarySelfSignature()
	if stale[0].PrimaryKey.KeyExpired(staleSelf, time.Now()) == false {
		t.Error("the registry's embedded copy is no longer expired — re-record the golden and revisit")
	}
	if refreshed[0].PrimaryKey.KeyExpired(freshSelf, time.Now()) {
		t.Error("the extended export is expired; re-fetch it from hashicorp.com")
	}
}

// A .sig file can carry several packets, and the one that verifies is
// not necessarily the first. Winding the clock back to a time claimed
// by a packet nobody verified would let anyone who can serve the .sig
// choose that clock, so the two must agree.
func TestVerifyAtSigningTimeRejectsMismatchedLeadPacket(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")

	// Both keys are generated three hours ago so they are valid across
	// the whole window. The real signature is two hours old; the decoy
	// prepended in front of it is only half an hour old, so winding the
	// clock to the decoy's time still verifies the real one — the case
	// the library's own signature-expiry backstop does not catch.
	atKeygen := pastConfig(-3 * time.Hour)
	published, err := openpgp.NewEntity("published", "sluice test", "pub@test.invalid", atKeygen)
	if err != nil {
		t.Fatalf("generating published key: %v", err)
	}
	decoy, err := openpgp.NewEntity("decoy", "sluice test", "decoy@test.invalid", atKeygen)
	if err != nil {
		t.Fatalf("generating decoy key: %v", err)
	}

	var realSig, decoySig bytes.Buffer
	if err := openpgp.DetachSign(&realSig, published, bytes.NewReader(sums), pastConfig(-2*time.Hour)); err != nil {
		t.Fatalf("signing: %v", err)
	}
	if err := openpgp.DetachSign(&decoySig, decoy, bytes.NewReader(sums), pastConfig(-30*time.Minute)); err != nil {
		t.Fatalf("signing decoy: %v", err)
	}
	combined := append(decoySig.Bytes(), realSig.Bytes()...)

	keyring, kerr := assembleKeyring(publishedKeys(t, published), nil)
	if kerr != nil {
		t.Fatalf("assembling keyring: %v", kerr)
	}
	_, err = verifyAtSigningTime(keyring, sums, combined)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("verifyAtSigningTime() = %v, want ErrSignature", err)
	}
	if !strings.Contains(err.Error(), "leads with a packet") {
		t.Errorf("error %q does not explain the packet mismatch", err)
	}
}
