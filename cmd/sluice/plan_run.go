// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/donaldgifford/sluice/internal/config"
	"github.com/donaldgifford/sluice/internal/mirror"
	"github.com/donaldgifford/sluice/internal/publish"
)

// errChangesPresent is plan's --detailed-exitcode signal: the plan
// rendered cleanly and contains changes. main maps it to exit 2 and
// prints nothing — the plan is already on stdout.
var errChangesPresent = errors.New("plan: changes present")

type planOpts struct {
	jsonOut  bool
	detailed bool
}

// computePlan is the shared read-diff step: plan uses it standalone,
// apply re-runs it inside its conditional-write window.
func computePlan(ctx context.Context, m *config.Manifest, b publish.Bucket) (*mirror.Plan, *publish.Actual, error) {
	declared := make([]string, 0, len(m.Providers))
	for _, p := range m.Providers {
		declared = append(declared, p.Source)
	}
	actual, err := publish.ReadActual(ctx, b, declared)
	if err != nil {
		return nil, nil, err
	}
	plan, err := mirror.Diff(mirror.Desired(m), actual.State)
	if err != nil {
		return nil, nil, err
	}
	return plan, actual, nil
}

func runPlan(ctx context.Context, m *config.Manifest, b publish.Bucket, opts planOpts, out io.Writer) error {
	plan, actual, err := computePlan(ctx, m, b)
	if err != nil {
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

	if opts.detailed && !plan.Empty() {
		return errChangesPresent
	}
	return nil
}

// renderPlan renders the human diff, grouped by provider in the
// plan's deterministic order. Removed versions look up their platform
// list in actual state — the plan itself only names them.
func renderPlan(p *mirror.Plan, actual mirror.State) string {
	if p.Empty() {
		return "No changes. Mirror matches the manifest.\n"
	}

	byProvider := make(map[string][]string)
	var order []string
	appendLine := func(provider, line string) {
		if _, seen := byProvider[provider]; !seen {
			order = append(order, provider)
		}
		byProvider[provider] = append(byProvider[provider], line)
	}

	for _, c := range p.Add {
		appendLine(c.Provider, fmt.Sprintf("  + %s  [%s]", c.Version, strings.Join(c.Platforms, ", ")))
	}
	for _, r := range p.Remove {
		plats := slices.Sorted(maps.Keys(actual[r.Provider][r.Version]))
		appendLine(r.Provider, fmt.Sprintf("  - %s  [%s]  (yank)", r.Version, strings.Join(plats, ", ")))
	}
	for _, c := range p.AddPlatform {
		appendLine(c.Provider, fmt.Sprintf("  ~ %s  +%s  (platform add)", c.Version, strings.Join(c.Platforms, " +")))
	}

	slices.Sort(order)
	var lines []string
	for _, provider := range order {
		lines = append(lines, provider)
		lines = append(lines, byProvider[provider]...)
		lines = append(lines, "")
	}
	lines = append(lines, fmt.Sprintf("Plan: %d to add, %d to remove, %d platform %s.",
		len(p.Add), len(p.Remove), len(p.AddPlatform), plural(len(p.AddPlatform), "change", "changes")))
	return strings.Join(lines, "\n") + "\n"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
