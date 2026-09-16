// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/publish"
)

type applyOpts struct {
	autoApprove      bool
	jsonOut          bool
	allowUnversioned bool
}

// applyDeps are apply's injected collaborators; production wiring
// lives in newApplyCmd, tests substitute fakes.
type applyDeps struct {
	bucket publish.Bucket
	fetch  publish.Fetcher
	signer *publish.Signer
	log    *slog.Logger
}

// runApply is the apply sequence: read → diff → render → confirm →
// execute. The read happens inside this process so the conditional
// index write guards the entire window, including the confirmation
// pause — there is no plan file to go stale.
func runApply(ctx context.Context, m *config.Manifest, deps applyDeps, opts applyOpts, in io.Reader, out io.Writer) error {
	if opts.jsonOut && !opts.autoApprove {
		return errors.New("--json is non-interactive and requires --auto-approve")
	}

	plan, actual, err := computePlan(ctx, m, deps.bucket)
	if err != nil {
		return err
	}
	if plan.Empty() {
		_, err := io.WriteString(out, "No changes. Mirror matches the manifest.\n")
		return err
	}

	rendered := renderPlan(plan, actual.State)
	if opts.jsonOut {
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding plan: %w", err)
		}
		rendered = string(data) + "\n"
	}
	if _, err := io.WriteString(out, rendered); err != nil {
		return err
	}

	if !opts.autoApprove {
		ok, err := confirm(in, out)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("apply cancelled — no changes were made")
		}
	}

	// Probe after confirmation so declined and empty runs stay
	// write-free; refusal still precedes fetch, sign, and every
	// mutation.
	caps, err := publish.Probe(ctx, deps.bucket)
	if err != nil {
		return err
	}
	if caps.Mode == publish.ModeDegraded && !opts.allowUnversioned {
		return fmt.Errorf("backend is %s: conditional writes or versioning unsupported — "+
			"re-run with --allow-unversioned-backend to proceed without the single-writer guarantee",
			caps.Summary())
	}

	return publish.NewApplier(deps.bucket, deps.fetch, deps.signer, deps.log, caps.Mode).Apply(ctx, plan, actual)
}

// confirm requires the literal "yes"; anything else — including EOF —
// declines.
func confirm(in io.Reader, out io.Writer) (bool, error) {
	if _, err := io.WriteString(out, "\nDo you want to apply these changes? Only 'yes' is accepted: "); err != nil {
		return false, err
	}
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("reading confirmation: %w", err)
		}
		return false, nil // EOF: decline
	}
	return strings.TrimSpace(scanner.Text()) == "yes", nil
}
