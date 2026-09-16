// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Mode is a backend capability tier from [Probe]: Full buckets version
// objects and honor conditional writes; Degraded buckets do at least
// one of neither (Garage as documented: preconditions
// accepted-and-ignored, no versioning).
type Mode string

const (
	// ModeFull is AWS and any compatible that versions and honors
	// preconditions — the single-writer guarantee holds.
	ModeFull Mode = "full"
	// ModeDegraded is everything else — conditional writes and
	// versioned audit refs are unavailable; apply fails closed
	// without an explicit opt-in.
	ModeDegraded Mode = "degraded"
)

// Capabilities is the probed backend profile. Full requires both
// versioning and honored preconditions; anything less is Degraded by
// construction, never by default.
type Capabilities struct {
	Mode         Mode
	Versioned    bool // Put returned a version ID
	Conditionals bool // If-None-Match/If-Match conflicts observed
}

// Summary renders the tier for plan verbose output and refusal
// errors: "full", or "degraded (…)" naming each missing capability.
func (c *Capabilities) Summary() string {
	if c.Mode == ModeFull {
		return string(ModeFull)
	}
	var missing []string
	if !c.Versioned {
		missing = append(missing, "no versioning")
	}
	if !c.Conditionals {
		missing = append(missing, "preconditions ignored")
	}
	return "degraded (" + strings.Join(missing, "; ") + ")"
}

// probePrefix holds scratch keys. Depth two keeps them invisible to
// source discovery (which only matches protocol-depth index.json),
// and every probe deletes its key — a crashed probe leaves inert,
// undiscoverable bytes, never phantom state.
const probePrefix = "_sluice/probe/"

// Probe determines the backend tier with a scratch-key round trip,
// behaviorally — through the same [Bucket] interface production uses,
// so no backend-specific API calls exist to maintain. A first
// If-None-Match put detects versioning (empty version ID means
// unversioned); repeating it plus a bogus-ETag If-Match detects
// whether preconditions are honored or silently ignored. Any I/O
// failure is an error: an undeterminable backend fails closed.
func Probe(ctx context.Context, b Bucket) (*Capabilities, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("probing backend: %w", err)
	}
	key := probePrefix + hex.EncodeToString(nonce[:])
	body := []byte("sluice backend capability probe")

	caps := &Capabilities{}
	cleanup := func() error {
		_, err := b.Delete(ctx, key)
		return err
	}

	vid, err := b.Put(ctx, key, "application/octet-stream", body, Cond{IfNoneMatch: true})
	if err != nil {
		return nil, fmt.Errorf("probing backend: first conditional put: %w", err)
	}
	caps.Versioned = vid != ""

	_, err = b.Put(ctx, key, "application/octet-stream", body, Cond{IfNoneMatch: true})
	if errors.Is(err, ErrConflict) {
		caps.Conditionals = true
	} else if err != nil {
		return nil, errors.Join(
			fmt.Errorf("probing backend: second conditional put: %w", err),
			cleanup())
	}
	// err == nil keeps Conditionals false: the backend overwrote
	// unconditionally, the Garage precondition-ignore shape.

	if caps.Conditionals {
		_, err = b.Put(ctx, key, "application/octet-stream", body, Cond{IfMatch: "sluice-probe-bogus-etag"})
		if err == nil {
			caps.Conditionals = false
		} else if !errors.Is(err, ErrConflict) {
			return nil, errors.Join(
				fmt.Errorf("probing backend: If-Match put: %w", err),
				cleanup())
		}
	}

	if err := cleanup(); err != nil {
		return nil, fmt.Errorf("probing backend: cleanup: %w", err)
	}

	if caps.Versioned && caps.Conditionals {
		caps.Mode = ModeFull
	} else {
		caps.Mode = ModeDegraded
	}
	return caps, nil
}
