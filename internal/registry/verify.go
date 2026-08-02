// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// verifySums checks the binary detached signature over the SHA256SUMS
// document using ONLY the keys the registry published in the download
// metadata, and returns the primary key ID of the signer that
// verified. The nil crypto config means library defaults: weak
// algorithms rejected, expired signing keys rejected, no insecure
// allowances anywhere.
func verifySums(keys []gpgKey, sums, sig []byte) (string, error) {
	keyring, err := assembleKeyring(keys)
	if err != nil {
		return "", err
	}
	signer, err := openpgp.CheckDetachedSignature(keyring, bytes.NewReader(sums), bytes.NewReader(sig), nil)
	if err != nil {
		// Covers both a corrupt/invalid signature and a signer absent
		// from the published keyring (unknown issuer).
		return "", fmt.Errorf("%w: %w", ErrSignature, err)
	}
	// Subkey signatures still report the primary key ID — the
	// identity the registry's key_id field and the audit trail speak.
	return signer.PrimaryKey.KeyIdString(), nil
}

// assembleKeyring parses every published ascii_armor block into one
// keyring. A key that fails to parse is a hard error, not skipped: a
// registry publishing garbage keys is a signal to surface, and
// silently narrowing the trusted set hides it.
func assembleKeyring(keys []gpgKey) (openpgp.EntityList, error) {
	var ring openpgp.EntityList
	for _, k := range keys {
		ents, err := openpgp.ReadArmoredKeyRing(strings.NewReader(k.ASCIIArmor))
		if err != nil {
			return nil, fmt.Errorf("parsing published signing key %q: %w", k.KeyID, err)
		}
		ring = append(ring, ents...)
	}
	if len(ring) == 0 {
		return nil, errors.New("registry published no usable signing keys")
	}
	return ring, nil
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
