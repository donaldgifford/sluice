// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/donaldgifford/sluice/internal/mirror"
)

// Actual is the bucket's observed state plus everything apply needs
// to rewrite it: raw version documents (preserved verbatim on
// add_platform), parsed indexes, and index ETags for the conditional
// write. A source absent from ETags has no index — first publish,
// written with If-None-Match.
type Actual struct {
	State mirror.State
	// Docs maps source → version → its current version document.
	Docs map[string]map[string]mirror.VersionDoc
	// Index maps source → its current parsed index.
	Index map[string]mirror.Index
	// ETags maps source → the index.json ETag observed at read time.
	ETags map[string]string
}

// ReadActual assembles the mirror's actual state. Discovery LISTs
// the bucket (a mirrored provider absent from the manifest must
// still be seen for removal to plan); content is then read from the
// discovered and declared indexes and the version documents they
// reference. A missing index is an unmirrored provider, not an
// error. An index referencing a missing or unparseable version
// document IS an error: sluice writes the index last, so that state
// cannot be sluice-authored — it is out-of-band corruption to
// surface, not to heal silently.
func ReadActual(ctx context.Context, b Bucket, declared []string) (*Actual, error) {
	keys, err := b.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("discovering mirrored providers: %w", err)
	}

	sources := discoverSources(keys)
	for _, d := range declared {
		if !slices.Contains(sources, d) {
			sources = append(sources, d)
		}
	}

	a := &Actual{
		State: make(mirror.State),
		Docs:  make(map[string]map[string]mirror.VersionDoc),
		Index: make(map[string]mirror.Index),
		ETags: make(map[string]string),
	}
	for _, src := range sources {
		if err := a.readProvider(ctx, b, src); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// readProvider loads one provider's index and referenced version
// documents into a.
func (a *Actual) readProvider(ctx context.Context, b Bucket, src string) error {
	body, etag, err := b.Get(ctx, indexKey(src))
	if errors.Is(err, ErrNotFound) {
		return nil // unmirrored: first publish
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", indexKey(src), err)
	}

	var idx mirror.Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return fmt.Errorf("parsing %s: %w", indexKey(src), err)
	}

	a.Index[src] = idx
	a.ETags[src] = etag
	a.Docs[src] = make(map[string]mirror.VersionDoc, len(idx.Versions))
	ps := make(mirror.ProviderState, len(idx.Versions))

	for ver := range idx.Versions {
		doc, err := readVersionDoc(ctx, b, src, ver)
		if err != nil {
			return err
		}
		a.Docs[src][ver] = doc
		set := make(mirror.PlatformSet, len(doc.Archives))
		for plat := range doc.Archives {
			set[plat] = struct{}{}
		}
		ps[ver] = set
	}
	a.State[src] = ps
	return nil
}

func readVersionDoc(ctx context.Context, b Bucket, src, ver string) (mirror.VersionDoc, error) {
	key := versionKey(src, ver)
	body, _, err := b.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return mirror.VersionDoc{}, fmt.Errorf(
			"index for %s lists %s but %s is missing — the mirror was modified outside sluice", src, ver, key)
	}
	if err != nil {
		return mirror.VersionDoc{}, fmt.Errorf("reading %s: %w", key, err)
	}
	var doc mirror.VersionDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return mirror.VersionDoc{}, fmt.Errorf("parsing %s: %w", key, err)
	}
	return doc, nil
}
