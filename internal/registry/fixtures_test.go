// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/donaldgifford/sluice/internal/config"
)

// newTestKey generates an ephemeral Ed25519 signing key. Keys are
// never committed to the repo: generation is sub-millisecond, no
// golden depends on key bytes, and ephemeral keys structurally prove
// the client trusts whatever keyring the metadata publishes rather
// than anything baked in.
func newTestKey(t *testing.T, name string) *openpgp.Entity {
	t.Helper()

	e, err := openpgp.NewEntity(name, "sluice test", name+"@test.invalid",
		&packet.Config{Algorithm: packet.PubKeyAlgoEdDSA})
	if err != nil {
		t.Fatalf("generating test key %s: %v", name, err)
	}
	return e
}

// armorPub renders the entity's public key as the ascii_armor block a
// registry publishes in download metadata.
func armorPub(t *testing.T, e *openpgp.Entity) string {
	t.Helper()

	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatalf("starting armor: %v", err)
	}
	if err := e.Serialize(w); err != nil {
		t.Fatalf("serializing public key: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing armor: %v", err)
	}
	return buf.String()
}

// signDetached produces a binary detached signature over msg — the
// SHA256SUMS.sig shape real registries serve.
func signDetached(t *testing.T, e *openpgp.Entity, msg []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := openpgp.DetachSign(&buf, e, bytes.NewReader(msg), nil); err != nil {
		t.Fatalf("signing: %v", err)
	}
	return buf.Bytes()
}

// newExpiredKeyAndSig generates a key that expired an hour ago (both
// key and signature are created on a clock two hours in the past,
// with a one-hour key lifetime) and a signature over msg made while
// the key was still valid. Verification with default config — real
// time — must reject it.
func newExpiredKeyAndSig(t *testing.T, msg []byte) (*openpgp.Entity, []byte) {
	t.Helper()

	cfg := &packet.Config{
		Algorithm:       packet.PubKeyAlgoEdDSA,
		Time:            func() time.Time { return time.Now().Add(-2 * time.Hour) },
		KeyLifetimeSecs: 3600,
	}
	e, err := openpgp.NewEntity("expired", "sluice test", "expired@test.invalid", cfg)
	if err != nil {
		t.Fatalf("generating expired key: %v", err)
	}
	var buf bytes.Buffer
	if err := openpgp.DetachSign(&buf, e, bytes.NewReader(msg), cfg); err != nil {
		t.Fatalf("signing with expired key: %v", err)
	}
	return e, buf.Bytes()
}

// revokeKey revokes the entity's primary key as of now, leaving any
// signature it already made intact. Models the case the expired-key
// override must never wave through: a key whose owner has since
// declared it compromised.
func revokeKey(t *testing.T, e *openpgp.Entity) {
	t.Helper()

	if err := e.RevokeKey(packet.KeyCompromised, "test revocation", nil); err != nil {
		t.Fatalf("revoking key: %v", err)
	}
}

// armorSig wraps a binary detached signature in ASCII armor — the
// .asc shape, which the client deliberately does not accept.
func armorSig(t *testing.T, sig []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	w, err := armor.Encode(&buf, "PGP SIGNATURE", nil)
	if err != nil {
		t.Fatalf("starting sig armor: %v", err)
	}
	if _, err := w.Write(sig); err != nil {
		t.Fatalf("writing sig armor: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing sig armor: %v", err)
	}
	return buf.Bytes()
}

// publishedKeys renders entities as the gpg_public_keys slice from download
// metadata.
func publishedKeys(t *testing.T, entities ...*openpgp.Entity) []gpgKey {
	t.Helper()

	keys := make([]gpgKey, 0, len(entities))
	for _, e := range entities {
		keys = append(keys, gpgKey{
			KeyID:      e.PrimaryKey.KeyIdString(),
			ASCIIArmor: armorPub(t, e),
		})
	}
	return keys
}

// zipArchive builds an in-memory zip from a name → content map.
func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("adding %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

// fakeRegistry is one self-consistent provider release behind an
// httptest server: metadata, zip, signed sums. Tests tamper by
// mutating fields after construction — the mux reads them at request
// time.
type fakeRegistry struct {
	client *Client
	server *httptest.Server

	source  string
	version string
	plat    config.Platform

	key       *openpgp.Entity // signs sums
	published []gpgKey        // keys the metadata advertises
	zipName   string
	zip       []byte
	sums      []byte
	sig       []byte

	// absZipURL, when set, becomes the metadata download_url verbatim
	// — the cross-host CDN shape. Otherwise a relative URL is served,
	// exercising ResolveReference.
	absZipURL string
	// shasum is the metadata shasum field, frozen at construction:
	// tampering the zip blob must NOT move the registry's metadata,
	// exactly as a blob-store attacker cannot rewrite the registry's
	// download response.
	shasum string
}

// newFakeRegistry builds a good release for source/version/plat whose
// zip holds files. Every part is consistent; tamper tests break one
// thing at a time.
func newFakeRegistry(t *testing.T, source, version string, plat config.Platform, files map[string]string) *fakeRegistry {
	t.Helper()

	addr, err := parseSource(source)
	if err != nil {
		t.Fatalf("bad fixture source %q: %v", source, err)
	}
	f := &fakeRegistry{
		source:  source,
		version: version,
		plat:    plat,
		key:     newTestKey(t, "release"),
		zipName: fmt.Sprintf("terraform-provider-%s_%s_%s.zip", addr.typ, version, plat),
		zip:     zipArchive(t, files),
	}
	f.published = publishedKeys(t, f.key)
	f.shasum = sha256Hex(f.zip)
	f.sums = []byte(f.shasum + "  " + f.zipName + "\n")
	f.sig = signDetached(t, f.key, f.sums)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/providers/{ns}/{type}/{version}/download/{os}/{arch}",
		func(w http.ResponseWriter, r *http.Request) {
			zipURL := "/files/" + f.zipName
			if f.absZipURL != "" {
				zipURL = f.absZipURL
			}
			body, err := json.Marshal(map[string]any{
				"download_url":          zipURL,
				"filename":              f.zipName,
				"shasums_url":           "/files/" + f.zipName + "_SHA256SUMS",
				"shasums_signature_url": "/files/" + f.zipName + "_SHA256SUMS.sig",
				"os":                    r.PathValue("os"),
				"arch":                  r.PathValue("arch"),
				"shasum":                f.shasum,
				"signing_keys":          map[string]any{"gpg_public_keys": f.published},
			})
			if err != nil {
				t.Errorf("marshaling metadata: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(body)
		})
	mux.HandleFunc("GET /files/{name}", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("name") {
		case f.zipName:
			_, _ = w.Write(f.zip)
		case f.zipName + "_SHA256SUMS":
			_, _ = w.Write(f.sums)
		case f.zipName + "_SHA256SUMS.sig":
			_, _ = w.Write(f.sig)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	f.client = New(WithBaseURL(func(string) string { return f.server.URL }))
	return f
}

// resign re-signs the current sums with the release key — for tamper
// cases that rewrite the sums document itself.
func (f *fakeRegistry) resign(t *testing.T) {
	t.Helper()
	f.sig = signDetached(t, f.key, f.sums)
}

// extendKey returns a second export of e whose self-signature runs a
// year out — the shape of an upstream key extension: re-cut
// self-signatures, same key material, same fingerprint. e itself is
// left untouched so callers keep the stale export to compare against.
func extendKey(t *testing.T, e *openpgp.Entity) *openpgp.Entity {
	t.Helper()

	copied := copyEntity(t, e)
	lifetime := uint32(365 * 24 * 3600)
	for _, id := range copied.Identities {
		id.SelfSignature.KeyLifetimeSecs = &lifetime
	}
	for i := range copied.Subkeys {
		copied.Subkeys[i].Sig.KeyLifetimeSecs = &lifetime
	}

	var out bytes.Buffer
	if err := copied.SerializePrivate(&out, nil); err != nil {
		t.Fatalf("re-signing extended key: %v", err)
	}
	ring, err := openpgp.ReadKeyRing(&out)
	if err != nil {
		t.Fatalf("reading extended key: %v", err)
	}
	return ring[0]
}

// copyEntity round-trips the entity through its private serialization
// without re-signing, yielding an independent copy.
func copyEntity(t *testing.T, e *openpgp.Entity) *openpgp.Entity {
	t.Helper()

	var buf bytes.Buffer
	if err := e.SerializePrivateWithoutSigning(&buf, nil); err != nil {
		t.Fatalf("serializing private key: %v", err)
	}
	ring, err := openpgp.ReadKeyRing(&buf)
	if err != nil {
		t.Fatalf("re-reading key: %v", err)
	}
	return ring[0]
}

// pastConfig is a crypto config whose clock is offset from now, for
// building keys and signatures with controlled creation times.
func pastConfig(offset time.Duration) *packet.Config {
	return &packet.Config{
		Algorithm: packet.PubKeyAlgoEdDSA,
		Time:      func() time.Time { return time.Now().Add(offset) },
	}
}
