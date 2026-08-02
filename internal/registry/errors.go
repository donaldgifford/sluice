// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"errors"
	"fmt"
)

// Sentinel classifications for verification failures. Callers match
// with errors.Is; the (provider, version, platform) tuple travels on
// the wrapping [Error].
var (
	// ErrSignature covers both an invalid SHA256SUMS signature and a
	// signature made by a key the registry did not publish.
	ErrSignature = errors.New("sums signature verification failed")

	// ErrChecksumMismatch means the downloaded zip's SHA-256 does not
	// match the entry in the GPG-verified SHA256SUMS.
	ErrChecksumMismatch = errors.New("zip sha256 does not match signed sums")

	// ErrSumsEntryMissing means the zip's filename has no entry in the
	// GPG-verified SHA256SUMS.
	ErrSumsEntryMissing = errors.New("zip filename not present in signed sums")
)

// Error is the failure type returned by [Client.FetchVerified]. It
// carries the tuple being verified and the step that failed so the
// caller can report precisely which artifact was rejected and why.
type Error struct {
	Source   string // "hostname/namespace/type"
	Version  string
	Platform string // "os_arch"
	Step     string // "metadata", "signature", "sums", "download", "hash"
	Err      error
}

func (e *Error) Error() string {
	return fmt.Sprintf("verify %s %s %s (%s): %v", e.Source, e.Version, e.Platform, e.Step, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
