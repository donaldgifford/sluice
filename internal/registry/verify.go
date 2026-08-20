// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	pgperrors "github.com/ProtonMail/go-crypto/openpgp/errors"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// verifySums checks the binary detached signature over the SHA256SUMS
// document using ONLY the keys the registry published in the download
// metadata, and returns the primary key ID of the signer that
// verified plus whether an expired key was accepted under the
// override. The nil crypto config means library defaults: weak
// algorithms rejected, expired signing keys rejected, no insecure
// allowances anywhere.
func verifySums(keys []gpgKey, sums, sig []byte, refreshed []string, allowExpired bool) (keyID string, expired bool, err error) {
	keyring, err := assembleKeyring(keys, refreshed)
	if err != nil {
		return "", false, err
	}
	signer, err := openpgp.CheckDetachedSignature(keyring, bytes.NewReader(sums), bytes.NewReader(sig), nil)
	if err == nil {
		// Subkey signatures still report the primary key ID — the
		// identity the registry's key_id field and the audit trail
		// speak.
		return signer.PrimaryKey.KeyIdString(), false, nil
	}
	// Only expiry is ever overridable, and only when the provider
	// opted in. Every other failure — bad signature, unknown issuer,
	// revoked key — stays fatal.
	if !errors.Is(err, pgperrors.ErrKeyExpired) {
		// Covers both a corrupt/invalid signature and a signer absent
		// from the published keyring (unknown issuer).
		return "", false, fmt.Errorf("%w: %w", ErrSignature, err)
	}
	if !allowExpired {
		// The usual cause is a registry serving a key export predating
		// an extension, so name the remedy rather than just the
		// symptom — including when a configured export matched nothing.
		return "", false, fmt.Errorf(
			"%w: %w — the registry's copy of this key is expired; supply the owner's "+
				"current export via mirror.signing_key_files, or set "+
				"allow_expired_signing_key on this provider",
			ErrSignature, err)
	}
	signer, err = verifyAtSigningTime(keyring, sums, sig)
	if err != nil {
		return "", false, err
	}
	return signer.PrimaryKey.KeyIdString(), true, nil
}

// verifyAtSigningTime re-checks the signature with the clock wound
// back to when the signature was made, which is what "the key was
// valid when it signed this" means. Revocation is deliberately NOT
// evaluated at that clock: a key revoked after signing is a key whose
// past signatures must stop being trusted, so revocation is re-checked
// against the real present.
func verifyAtSigningTime(keyring openpgp.EntityList, sums, sig []byte) (*openpgp.Entity, error) {
	claimed, err := sigCreationTime(sig)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSignature, err)
	}
	now := time.Now()
	if claimed.After(now) {
		return nil, fmt.Errorf("%w: signature claims a creation time in the future (%s)",
			ErrSignature, claimed.UTC().Format(time.RFC3339))
	}

	// The clock is wound back to the time claimed by the first packet,
	// but a .sig file can carry several — so verify, then confirm the
	// packet that actually verified is the one that time came from.
	cfg := &packet.Config{Time: func() time.Time { return claimed }}
	verified, signer, err := openpgp.VerifyDetachedSignature(
		keyring, bytes.NewReader(sums), bytes.NewReader(sig), cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSignature, err)
	}
	if !verified.CreationTime.Equal(claimed) {
		return nil, fmt.Errorf(
			"%w: signature file leads with a packet made %s but verifies with one made %s",
			ErrSignature, claimed.UTC().Format(time.RFC3339),
			verified.CreationTime.UTC().Format(time.RFC3339))
	}
	if revokedNow(signer, now) {
		return nil, fmt.Errorf("%w: signing key %s is revoked",
			ErrSignature, signer.PrimaryKey.KeyIdString())
	}
	return signer, nil
}

// revokedNow reports whether any part of the entity a verifier would
// rely on — primary key, a user identity, or a subkey — is revoked as
// of now.
func revokedNow(e *openpgp.Entity, now time.Time) bool {
	if e.Revoked(now) {
		return true
	}
	for _, id := range e.Identities {
		if id.Revoked(now) {
			return true
		}
	}
	for i := range e.Subkeys {
		if e.Subkeys[i].Revoked(now) {
			return true
		}
	}
	return false
}

// sigCreationTime reads the creation time the detached signature
// claims. It is signer-controlled, which is acceptable here only
// because anyone holding the private key could backdate a signature
// regardless — revocation, not expiry, is what defends against a
// compromised key.
func sigCreationTime(sig []byte) (time.Time, error) {
	p, err := packet.Read(bytes.NewReader(sig))
	if err != nil {
		return time.Time{}, fmt.Errorf("reading signature packet: %w", err)
	}
	s, ok := p.(*packet.Signature)
	if !ok {
		return time.Time{}, fmt.Errorf("first packet is %T, not a signature", p)
	}
	return s.CreationTime, nil
}

// assembleKeyring parses every published ascii_armor block into one
// keyring, substituting any refreshed export of the same key. A key
// that fails to parse is a hard error, not skipped: a registry
// publishing garbage keys is a signal to surface, and silently
// narrowing the trusted set hides it.
func assembleKeyring(keys []gpgKey, refreshed []string) (openpgp.EntityList, error) {
	updates, err := parseRefreshed(refreshed)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	var ring openpgp.EntityList
	for _, k := range keys {
		ents, err := openpgp.ReadArmoredKeyRing(strings.NewReader(k.ASCIIArmor))
		if err != nil {
			return nil, fmt.Errorf("parsing published signing key %q: %w", k.KeyID, err)
		}
		for _, e := range ents {
			entity, err := refreshEntity(e, updates, now)
			if err != nil {
				return nil, err
			}
			ring = append(ring, entity)
		}
	}
	if len(ring) == 0 {
		return nil, errors.New("registry published no usable signing keys")
	}
	// A refreshed export that matches nothing here is not an error:
	// the configured set is mirror-wide, so an export for one
	// registry's key legitimately matches nothing while fetching from
	// another. A misconfigured export surfaces as the expiry failure
	// it was meant to prevent, which names the remedy.
	return ring, nil
}

// refreshEntity swaps in an operator-supplied export of e when one
// carries the same primary-key fingerprint. Owners extend a key by
// publishing new self-signatures, so a registry serving a stale export
// reports an expiry its own key material has long since moved past.
// Matching on the full fingerprint is what keeps this from being a
// trust decision: the set of trusted keys is still exactly the set the
// registry published, only its validity metadata is current.
//
// Substitution may only ever add validity, never subtract it. An
// operator export predating a revocation the registry now carries
// would otherwise un-revoke a compromised key — precisely the attack
// revocation exists to stop — so a revoked published key is refused
// outright rather than replaced.
func refreshEntity(
	e *openpgp.Entity, updates openpgp.EntityList, now time.Time,
) (*openpgp.Entity, error) {
	for _, u := range updates {
		if !bytes.Equal(u.PrimaryKey.Fingerprint, e.PrimaryKey.Fingerprint) {
			continue
		}
		if revokedNow(e, now) {
			return nil, fmt.Errorf(
				"%w: registry published signing key %s as revoked; refusing the refreshed export",
				ErrSignature, e.PrimaryKey.KeyIdString())
		}
		return u, nil
	}
	return e, nil
}

// parseRefreshed reads the operator-supplied armored key exports. An
// unparseable export is a hard error: it was configured deliberately,
// so silently ignoring it would leave verification quietly running on
// the stale copy it was meant to replace.
func parseRefreshed(refreshed []string) (openpgp.EntityList, error) {
	var out openpgp.EntityList
	for i, armored := range refreshed {
		ents, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
		if err != nil {
			return nil, fmt.Errorf("parsing refreshed signing key %d: %w", i+1, err)
		}
		out = append(out, ents...)
	}
	return out, nil
}

// sumsEntry returns filename's SHA-256 from a GPG-verified SHA256SUMS
// document. The format is strict — 64 lowercase hex chars, two
// spaces, exact filename — and ambiguity (the filename listed twice
// with differing hashes) is treated as tampering, not a tie to break.
func sumsEntry(sums []byte, filename string) (string, error) {
	var found string
	for i, line := range strings.Split(string(sums), "\n") {
		if line == "" {
			continue
		}
		hash, name, ok := parseSumsLine(line)
		if !ok {
			return "", fmt.Errorf("malformed SHA256SUMS line %d", i+1)
		}
		if name != filename {
			continue
		}
		if found != "" && found != hash {
			return "", fmt.Errorf("SHA256SUMS lists %q twice with differing hashes", filename)
		}
		found = hash
	}
	if found == "" {
		return "", fmt.Errorf("%w: %q", ErrSumsEntryMissing, filename)
	}
	return found, nil
}

// parseSumsLine splits "<64 lowercase hex>  <name>". No "*" binary
// marker, no CRLF, no uppercase — strictness is the closed default
// until a real registry demonstrates the need.
func parseSumsLine(line string) (hash, name string, ok bool) {
	const hexLen = 64
	if len(line) < hexLen+3 || line[hexLen] != ' ' || line[hexLen+1] != ' ' {
		return "", "", false
	}
	hash, name = line[:hexLen], line[hexLen+2:]
	for i := 0; i < len(hash); i++ {
		c := hash[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", "", false
		}
	}
	return hash, name, true
}
