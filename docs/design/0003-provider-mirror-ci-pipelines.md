---
id: DESIGN-0003
title: "Provider Mirror CI Pipelines"
status: Draft
author: Donald Gifford
created: 2026-07-27
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0003: Provider Mirror CI Pipelines

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [CLI repository pipeline](#cli-repository-pipeline)
  - [Approved-versions repository pipeline](#approved-versions-repository-pipeline)
  - [Renovate wiring](#renovate-wiring)
  - [Failure handling](#failure-handling)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

**Status:** Draft **Author:** Donald **Date:** 2026-07-26

## Overview

The automation around the mirror: the CLI's own build/test/release pipeline, and
the approved-versions repo's operational pipeline where plan/apply runs, drift
detection, and Renovate's two-stage version flow live. Together these make the
mirror hands-off: humans only ever review PRs.

## Goals and Non-Goals

### Goals

- CLI releases are reproducible, versioned, and distributed through the standard
  internal tooling channel.
- Approved-versions repo: plan-on-PR with a readable diff comment,
  apply-on-merge under a least-privilege OIDC role, event-driven integrity
  verification with a scheduled drift backstop.
- Single-writer guarantee enforced by CI, not convention.
- Renovate automates both inflow (upstream → approval PRs) and outflow (mirror →
  module repos).

### Non-Goals

- Policy enforcement pipelines (covered by the policy library design).
- Atlantis-side configuration (covered in the RFC cutover phase).

## Background

Publisher IAM role and bucket come from the mirror bucket module. The CLI's
`-detailed-exitcode` and `-json` plan output exist specifically for these
workflows. Runners executing `plan`/`apply` must reach the bucket through the
VPC endpoint, so these jobs are pinned to self-hosted VPC runners.

## Detailed Design

### CLI repository pipeline

- **PR/main:** `go test ./...` with `-race`, LocalStack service container for
  integration tests, lint per the Go standards, plus the e2e job (apply to
  LocalStack → `terraform init` against it).
- **Release (tag):** goreleaser builds platform binaries and a container image;
  image pinned by digest wherever workflows consume it.
- **Distribution:** publish through whatever lands from the internal tool
  registry RFC (mise/aqua/S3-backed manifest); the release job uploads to that
  channel so developer machines and CI resolve the same binary.

### Approved-versions repository pipeline

- **PR:** `validate` → `plan -json -detailed-exitcode`. A comment bot renders
  the JSON diff as an add/remove table per provider, reusing the existing
  Atlantis comment patterns; a sluice-shipped composite action consuming the
  same JSON is a planned follow-on, reusable as a standalone check in module
  repos. Exit 2 is the normal "changes present" path; exit 1 fails the check.
  Read-only role (no publish permissions on PR builds — plans only read index
  files).
- **Merge to main:** `apply -auto-approve` under the publisher OIDC role, trust
  condition locked to `repo:<org>/approved-providers:ref:refs/heads/main`,
  wrapped in a GitHub environment with required reviewers disabled (the PR
  review _was_ the approval) but deployment logging on. The workflow then
  attests the applied change: cosign over the `-json` plan plus outcome,
  uploaded under a `_meta/` prefix — run-level evidence binding change set → PR
  → CI run (Open Questions 2).
- **Concurrency:** a single `concurrency` group across plan-on-main and apply
  cancels nothing and queues everything — combined with the CLI's conditional
  index writes, this is the single-writer guarantee.
- **Integrity verification (event-driven, primary):** the CloudTrail write data
  events on the bucket (enabled per the bucket-module logging decision) feed an
  EventBridge rule → verifier Lambda. On every write it checks the writing
  principal is the publisher role and, for artifacts, that a valid cosign
  signature/attestation exists (with a short grace window, since sig objects
  land after their zips). Any failure pages the platform channel and opens an
  incident issue within seconds of the write.
- **Drift (scheduled, backstop):** daily `plan -detailed-exitcode`; exit 2
  opens/updates a drift issue and pages. This is the level-triggered safety net
  behind the edge-triggered verifier — it catches whatever the event path misses
  (undelivered events, a disabled rule or broken Lambda, drift predating the
  notification config) by reconciling the whole bucket against the manifest.
  Drift here means out-of-band bucket modification — always an incident signal,
  never routine.

### Renovate wiring

- **Inflow (this repo):** a custom manager (regex over the `versions = [...]`
  lists) with the `terraform-provider` datasource proposes new upstream versions
  as PRs. Merging is the approval act.
- **Outflow (module/infra repos):** a `customDatasource` per provider pointed at
  the mirror's `index.json` (jsonata: keys of `.versions` → releases).
  Developer-facing PRs can only ever reference versions that survived
  verification and review.

### Failure handling

- Apply is idempotent: rerun on partial failure republishes staged artifacts;
  index-last ordering means consumers never saw the partial state.
- Conditional-write conflict aborts with a distinct exit; the workflow re-runs
  plan and fails the job with the fresh diff for human eyes.
- Registry outage during apply: retry with backoff, then fail — the mirror keeps
  serving its current contents throughout, which is the point.

## API / Interface Changes

New repos/workflows only. The plan-comment JSON schema from the CLI design is
the contract between tool and bot.

## Data Model

None. All state remains HCL (git) and protocol JSON (S3).

## Testing Strategy

- Workflow logic exercised in the CLI repo's e2e job (same steps, LocalStack).
- A permanent canary entry (e.g. `hashicorp/null`) in the approved-versions
  manifest gives every apply and every drift plan a real provider to verify
  end-to-end.
- Quarterly game-day: simulate an out-of-band bucket write and confirm the
  event-driven verifier pages within seconds; then repeat with the EventBridge
  rule disabled to prove the scheduled backstop catches it — the backstop only
  earns its keep if it is actually exercised.

## Migration / Rollout Plan

1. CLI release pipeline first (needed before anything consumes it).
2. Approved-versions repo with plan-only PR checks; seed via `bootstrap`.
3. Enable apply-on-merge; run one supervised merge.
4. Enable the event-driven verifier and the daily drift backstop, plus Renovate
   inflow; outflow datasource rolls out with the org-wide cutover.

## Open Questions

1. **PR comment bot — where does the plan renderer live?** **Resolved
   (2026-07-31): (c), with (a) as a follow-on.** Reuse the existing Atlantis
   comment patterns for the PR diff comment — lowest lift, and the reviewer
   experience is already familiar. Separately build the sluice-repo composite
   action from (a) as a validation surface, expanded into a reusable check that
   module repos can run in their own CI against the same `-json` contract.

2. **Signed apply summary — attest each applied plan, or are the deployment log
   and CloudTrail enough?** **Resolved (2026-07-31): (b), day one.** Every apply
   publishes a cosign attestation over the `-json` plan plus outcome under a
   `_meta/` prefix — run-level evidence binding change set → PR → CI run. Scope
   note recorded with the decision: this is audit evidence for mirror changes,
   not a bypass detector — nothing in the terraform execution path verifies plan
   signatures; out-of-band terraform applies are prevented by IAM (pipeline-only
   apply credentials) and detected by CloudTrail, not by this attestation.

3. **Runners for the drift schedule — dedicated pool or piggyback?** **Resolved
   (2026-07-31): largely superseded by event-driven verification.** Primary
   detection moved to the CloudTrail-events → EventBridge → verifier Lambda path
   (see Detailed Design), which needs no runners at all. The remaining daily
   backstop plan piggybacks the existing VPC runner pool — a read-only job
   measured in seconds needs no dedicated capacity.

## References

- DESIGN: Provider Mirror CLI; DESIGN: Provider Mirror Bucket Terraform Module
- RFC: Terraform Provider Cache and Internal Provider Mirror
- Internal tool registry RFC (distribution channel)
