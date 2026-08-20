// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"context"
	"log/slog"
)

// auditEntry is one publish/retract event. Every field is always
// emitted; empty strings mark the unknowable (sha256 and signing key
// on retract — the mirror protocol stores neither; s3_version_id on
// unversioned buckets, where its emptiness is itself a signal).
type auditEntry struct {
	action       string // "publish" or "retract"
	provider     string
	version      string
	platform     string
	h1           string
	sha256       string
	signingKeyID string
	s3VersionID  string
}

// audit emits the structured audit line. The schema is a contract —
// asserted key-for-key in tests; changing it breaks downstream log
// pipelines.
func (a *Applier) audit(ctx context.Context, e *auditEntry) {
	a.log.LogAttrs(ctx, slog.LevelInfo, "audit",
		slog.String("action", e.action),
		slog.String("provider", e.provider),
		slog.String("version", e.version),
		slog.String("platform", e.platform),
		slog.String("h1", e.h1),
		slog.String("sha256", e.sha256),
		slog.String("signing_key_id", e.signingKeyID),
		slog.String("s3_version_id", e.s3VersionID),
	)
}
