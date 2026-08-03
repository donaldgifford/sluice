// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/mirror"
	"github.com/donaldgifford/sluice/internal/registry"
)

// Fetcher is the verified-fetch surface the applier consumes —
// registry.Client in production.
type Fetcher interface {
	FetchVerified(ctx context.Context, source, version string, platform config.Platform, destDir string) (*registry.Artifact, error)
}

// uploadSuffixes are the signing outputs published alongside a zip
// when present (key-based signing produces no certificate, keyless
// does).
var uploadSuffixes = []string{".sig", ".pem", ".intoto.jsonl"}

// Applier executes a plan against the bucket. Construct with
// [NewApplier].
type Applier struct {
	bucket Bucket
	fetch  Fetcher
	signer *Signer
	log    *slog.Logger
}

// NewApplier wires the applier's dependencies. log receives the
// audit lines; pass slog.Default() in production.
func NewApplier(b Bucket, f Fetcher, s *Signer, log *slog.Logger) *Applier {
	return &Applier{bucket: b, fetch: f, signer: s, log: log}
}

// Apply executes the plan. Per provider, in the plan's deterministic
// order: fetch + verify + sign every artifact, upload zips and
// signing outputs, write version documents, then commit with a
// conditional index.json write — the index is written last so a
// crash at any earlier step leaves observable state unchanged and a
// rerun converges. Removal rewrites the index first, then deletes
// version documents; zips stay for forensics. A conditional-write
// failure aborts the whole apply with [ErrConflict] (exit 3): a
// concurrent writer invalidates every remaining assumption.
func (a *Applier) Apply(ctx context.Context, plan *mirror.Plan, actual *Actual) (err error) {
	if plan.Empty() {
		return nil
	}
	// Fail closed before any fetch or write: no cosign, no apply.
	if err := a.signer.Preflight(ctx); err != nil {
		return err
	}

	staging, err := os.MkdirTemp("", "sluice-apply-*")
	if err != nil {
		return fmt.Errorf("creating staging directory: %w", err)
	}
	defer func() {
		if rerr := os.RemoveAll(staging); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}()

	for _, w := range groupByProvider(plan) {
		if err := a.applyProvider(ctx, w, actual, staging); err != nil {
			return fmt.Errorf("publishing %s: %w", w.source, err)
		}
	}
	return nil
}

// providerWork is one provider's slice of the plan. fetches holds
// (version, platforms) pairs in plan order across both adds and
// platform additions; newVersions are the index additions.
type providerWork struct {
	source      string
	fetches     []fetchItem
	newVersions []string
	removes     []string
}

type fetchItem struct {
	version   string
	platforms []string
	isAdd     bool // whole new version vs platforms joining an existing one
}

// groupByProvider slices the plan per provider, preserving the
// plan's deterministic provider and version ordering.
func groupByProvider(plan *mirror.Plan) []*providerWork {
	bySrc := make(map[string]*providerWork)
	var order []*providerWork
	get := func(src string) *providerWork {
		if w, ok := bySrc[src]; ok {
			return w
		}
		w := &providerWork{source: src}
		bySrc[src] = w
		order = append(order, w)
		return w
	}

	for _, c := range plan.Add {
		w := get(c.Provider)
		w.fetches = append(w.fetches, fetchItem{version: c.Version, platforms: c.Platforms, isAdd: true})
		w.newVersions = append(w.newVersions, c.Version)
	}
	for _, c := range plan.AddPlatform {
		w := get(c.Provider)
		w.fetches = append(w.fetches, fetchItem{version: c.Version, platforms: c.Platforms})
	}
	for _, r := range plan.Remove {
		get(r.Provider).removes = append(get(r.Provider).removes, r.Version)
	}

	slices.SortFunc(order, func(a, b *providerWork) int {
		switch {
		case a.source < b.source:
			return -1
		case a.source > b.source:
			return 1
		default:
			return 0
		}
	})
	return order
}

// applyProvider runs one provider's full sequence: fetch+sign →
// upload artifacts → version docs → conditional index → removal
// deletes.
func (a *Applier) applyProvider(ctx context.Context, w *providerWork, actual *Actual, staging string) error {
	destDir, err := os.MkdirTemp(staging, "provider-*")
	if err != nil {
		return fmt.Errorf("creating provider staging: %w", err)
	}

	artifacts, err := a.fetchAndSign(ctx, w, destDir)
	if err != nil {
		return err
	}
	if err := a.uploadAll(ctx, w, artifacts); err != nil {
		return err
	}
	if err := a.writeVersionDocs(ctx, w, actual, artifacts); err != nil {
		return err
	}
	if err := a.commitIndex(ctx, w, actual); err != nil {
		return err
	}
	return a.retract(ctx, w, actual)
}

// fetchAndSign verifies and signs every artifact before anything is
// uploaded: a verification or signing failure leaves this provider
// entirely unpublished.
func (a *Applier) fetchAndSign(ctx context.Context, w *providerWork, destDir string) (map[string][]*registry.Artifact, error) {
	artifacts := make(map[string][]*registry.Artifact)
	for _, item := range w.fetches {
		for _, plat := range item.platforms {
			p, err := config.ParsePlatform(plat)
			if err != nil {
				return nil, fmt.Errorf("plan platform %q: %w", plat, err)
			}
			art, err := a.fetch.FetchVerified(ctx, w.source, item.version, p, destDir)
			if err != nil {
				return nil, err
			}
			if art.SigningKeyExpired {
				// Verified only because the provider opted in. Say so
				// on every artifact it covers, not once per run.
				a.log.WarnContext(ctx, "upstream signing key is expired",
					slog.String("provider", w.source),
					slog.String("version", art.Version),
					slog.String("platform", art.Platform.String()),
					slog.String("signing_key_id", art.SigningKeyID))
			}
			if err := a.signer.SignAndAttest(ctx, art); err != nil {
				return nil, err
			}
			artifacts[item.version] = append(artifacts[item.version], art)
		}
	}
	return artifacts, nil
}

// uploadAll puts zips and signing outputs, auditing each publish
// with the zip put's version ID.
func (a *Applier) uploadAll(ctx context.Context, w *providerWork, artifacts map[string][]*registry.Artifact) error {
	for _, item := range w.fetches {
		for _, art := range artifacts[item.version] {
			vid, err := a.uploadArtifact(ctx, w.source, art)
			if err != nil {
				return err
			}
			a.audit(ctx, &auditEntry{
				action: "publish", provider: w.source, version: art.Version,
				platform: art.Platform.String(), h1: art.H1, sha256: art.SHA256,
				signingKeyID: art.SigningKeyID, s3VersionID: vid,
			})
		}
	}
	return nil
}

// writeVersionDocs writes full documents for new versions and merged
// documents for platform additions — existing archives preserved
// verbatim.
func (a *Applier) writeVersionDocs(ctx context.Context, w *providerWork, actual *Actual, artifacts map[string][]*registry.Artifact) error {
	for _, item := range w.fetches {
		doc := mirror.VersionDoc{Archives: make(map[string]mirror.Archive)}
		if !item.isAdd {
			doc.Archives = maps.Clone(actual.Docs[w.source][item.version].Archives)
		}
		for _, art := range artifacts[item.version] {
			doc.Archives[art.Platform.String()] = mirror.Archive{
				URL:    art.Filename,
				Hashes: []string{art.H1},
			}
		}
		body, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("encoding %s: %w", versionKey(w.source, item.version), err)
		}
		if _, err := a.bucket.Put(ctx, versionKey(w.source, item.version), "application/json", body, Cond{}); err != nil {
			return err
		}
	}
	return nil
}

// retract deletes removed version documents after the index rewrite:
// consumers never see a listed-but-missing version document, and our
// own fail-closed reader never trips on sluice-authored state.
func (a *Applier) retract(ctx context.Context, w *providerWork, actual *Actual) error {
	for _, ver := range w.removes {
		vid, err := a.bucket.Delete(ctx, versionKey(w.source, ver))
		if err != nil {
			return err
		}
		for plat, doc := range actual.Docs[w.source][ver].Archives {
			h1 := ""
			if len(doc.Hashes) > 0 {
				h1 = doc.Hashes[0]
			}
			a.audit(ctx, &auditEntry{
				action: "retract", provider: w.source, version: ver,
				platform: plat, h1: h1, s3VersionID: vid,
			})
		}
	}
	return nil
}

// uploadArtifact puts the zip and its signing outputs, returning the
// zip put's S3 version ID for the audit line.
func (a *Applier) uploadArtifact(ctx context.Context, src string, art *registry.Artifact) (string, error) {
	body, err := os.ReadFile(art.Path)
	if err != nil {
		return "", fmt.Errorf("reading staged %s: %w", art.Filename, err)
	}
	vid, err := a.bucket.Put(ctx, zipKey(src, art.Filename), "application/zip", body, Cond{})
	if err != nil {
		return "", err
	}
	for _, suffix := range uploadSuffixes {
		out, err := os.ReadFile(art.Path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue // key-based signing emits no certificate
		}
		if err != nil {
			return "", fmt.Errorf("reading signing output %s: %w", filepath.Base(art.Path+suffix), err)
		}
		if len(out) == 0 {
			continue
		}
		key := zipKey(src, art.Filename) + suffix
		if _, err := a.bucket.Put(ctx, key, "application/octet-stream", out, Cond{}); err != nil {
			return "", err
		}
	}
	return vid, nil
}

// commitIndex writes the provider's index — the commit point — with
// the conditional matching how the index was observed: If-Match on
// the captured ETag, or If-None-Match for a first publish. One write
// reflects both additions and removals atomically.
func (a *Applier) commitIndex(ctx context.Context, w *providerWork, actual *Actual) error {
	idx := mirror.Index{Versions: make(map[string]mirror.IndexEntry)}
	if cur, ok := actual.Index[w.source]; ok {
		idx.Versions = maps.Clone(cur.Versions)
		if idx.Versions == nil {
			idx.Versions = make(map[string]mirror.IndexEntry)
		}
	}
	for _, ver := range w.newVersions {
		idx.Versions[ver] = mirror.IndexEntry{}
	}
	for _, ver := range w.removes {
		delete(idx.Versions, ver)
	}

	body, err := json.Marshal(idx)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", indexKey(w.source), err)
	}

	cond := Cond{IfNoneMatch: true}
	if etag, ok := actual.ETags[w.source]; ok {
		cond = Cond{IfMatch: etag}
	}
	if _, err := a.bucket.Put(ctx, indexKey(w.source), "application/json", body, cond); err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("index.json changed since the plan was computed — re-run sluice plan and apply: %w", err)
		}
		return err
	}
	return nil
}
