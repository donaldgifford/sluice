// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"bytes"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
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

// publishedKeys renders entities as the gpg_keys slice from download
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
