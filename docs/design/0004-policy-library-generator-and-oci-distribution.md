---
id: DESIGN-0004
title: "Policy Library, Generator, and OCI Distribution"
status: Draft
author: Donald Gifford
created: 2026-07-27
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0004: Policy Library, Generator, and OCI Distribution

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Repository layout](#repository-layout)
  - [Conventions](#conventions)
  - [Generator (Go, hclkit)](#generator-go-hclkit)
  - [Testing](#testing)
  - [Distribution](#distribution)
  - [Module source governance](#module-source-governance)
  - [Worked example: mirror allowlist policies](#worked-example-mirror-allowlist-policies)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

**Status:** Draft **Author:** Donald **Date:** 2026-07-26

## Overview

The concrete design behind the infrastructure-compliance RFC: the policy repo's
structure and conventions, a Go generator that produces data-driven checks from
HCL definitions, and the release pipeline that ships policies as signed OCI
artifacts consumed by Atlantis, repo-guardian, and CI.

## Goals and Non-Goals

### Goals

- One repo, clear domain layout, every policy tested and documented.
- Data-driven checks (allowlists) generated from systems of record — never
  hand-maintained in Rego.
- Bundles distributed as semver-tagged, cosign-signed OCI artifacts; consumers
  pin and verify.
- Module source governance implementable as plan-JSON policies from day one.

### Non-Goals

- A policy decision service (no OPA server); evaluation is always local via
  Conftest/OPA.
- Kubernetes admission policies (later domain; layout leaves room).
- The exceptions service itself (separate project; this consumes its export).

## Background

Conftest supports `conftest push`/`conftest pull` of policy bundles as OCI
artifacts (ORAS under the hood), which any internal OCI registry can store
alongside images. Rego policies evaluate Terraform plan JSON (Atlantis policy
stage), raw HCL, or arbitrary structured input (repo-guardian). The
approved-providers HCL manifest from the mirror project is the first
system-of-record data source.

## Detailed Design

### Repository layout

```
policy/
  provider/        # mirror allowlist, version governance
  module-source/   # module origin governance
  baseline/        # encryption, tagging, public exposure
  hygiene/         # lockfiles, terragrunt, workflow rules
  <domain>/*.rego + *_test.rego
data/              # GENERATED — never hand-edited
defs/              # HCL definitions consumed by the generator
docs/policies/     # GENERATED per-policy docs
bundle.yaml        # bundle metadata (version, included domains)
```

### Conventions

- One rule concern per file; package `policy.<domain>.<name>`.
- Every rule carries OPA metadata annotations (title, description, severity,
  remediation URL) — the generator renders `docs/policies/` from these, so
  undocumented rules fail CI structurally.
- Severity via rule name: `deny_*` blocks, `warn_*` advises. Promotion from warn
  to deny is a one-line diff with git history as the record.
- Every deny/warn result returns a stable code (`PROV001`) for dashboards and
  exception matching.

### Generator (Go, hclkit)

The generator turns HCL definitions in `defs/` into the machinery around
hand-written logic:

- **Data documents:** `data/providers.json` comes straight from `sluice export`
  against the manifest in this repo — no generator logic involved; the generator
  renders the rest (`data/module_origins.json`, and `data/exceptions.json`
  pulled from the exceptions service export) at bundle build time. Policies
  reference `data.*` only.
- **No Rego, by design:** the generator never emits rules. Policy logic is
  hand-written once per domain and owned as source — the rules are small
  membership checks over inputs conftest already parses, so codegen would add a
  second rule provenance and review noise for zero enforcement gain.
- **Docs:** renders the per-policy markdown and the README index from metadata
  annotations.

Generated files are committed and CI verifies `generate --check` produces no
diff — reviewers see data changes in PRs, and bundle builds are reproducible
from the repo alone.

### Testing

- `opa test` with coverage threshold enforced in CI.
- Fixture corpus: real `terraform show -json` plan outputs captured from
  representative repos (a libtftest helper produces these), plus minimal
  synthetic fixtures per rule including expected-pass cases — policies are
  tested for what they allow, not just what they block.
- A contract test evaluates the built bundle exactly as Atlantis will
  (`conftest test --update oci://…` against fixtures) so packaging bugs fail in
  CI, not in the policy stage.

### Distribution

- Release tag → CI builds the bundle (generate, test, package),
  `conftest push oci://<registry>/policy/infra:<semver>`, cosign-signs the
  artifact, and also moves a `stable` tag.
- **Registry contract:** the concrete registry is deferred (ECR or GitHub
  Packages — OCI either way). The repo ships a setup guide per candidate
  enumerating what it must provide for this pipeline: ORAS-style OCI artifact
  support for `conftest push`/`pull`, cosign signature storage alongside the
  artifact, push auth from Actions (OIDC to ECR / `GITHUB_TOKEN` to ghcr), pull
  auth for Atlantis, repo-guardian, and module-repo CI, tag immutability or
  equivalent retention, and compatibility with Renovate's OCI datasource for pin
  bumps. Whichever registry is chosen must satisfy the guide before cutover.
- Consumers pin the semver digest and verify the cosign signature before
  evaluation; Renovate's OCI datasource bumps pins.
- Atlantis: pre-workflow hook (or baked into the image at digest) pulls and
  verifies the pinned bundle; the policy stage points at the local pull.
  Registry-down behavior: evaluate the last verified cached bundle (fail-open on
  _distribution_, never on _evaluation_) — recorded as the availability decision
  from the RFC risk table.
- repo-guardian: embeds the same pull/verify library and evaluates repo content
  against the same bundle version.

### Module source governance

Plan JSON exposes every module call's source address (`configuration` →
`module_calls`, recursively). The policy allows: the internal module registry
hostname, and `git::` sources in approved orgs pinned to a tag (`?ref=v…`); it
denies unpinned refs, unknown hosts, and raw HTTPS archives. Origin patterns
live in `data/module_origins.json` via `defs/`. Ships as `warn_*` with the
violation dashboard sizing the migration; the registry publication path for
module authors is a prerequisite for promotion to deny. When the module registry
work matures (or OpenTofu's OCI-native module distribution becomes relevant),
only the data document changes.

### Worked example: mirror allowlist policies

Documentation-level examples of the first policies in the library — one
generated data document, three small rules, four places they run. Parsed input
shapes should be confirmed with `conftest parse` during implementation.

`data/providers.json` — the output of `sluice export`, committed and kept honest
by a `--check` diff in CI:

```json
{
  "providers": {
    "registry.terraform.io/hashicorp/aws": ["6.2.0", "6.3.0"],
    "registry.opentofu.org/hashicorp/null": ["3.2.4"]
  }
}
```

**Lock-file policy** (the portable one — works anywhere a `.terraform.lock.hcl`
exists: Atlantis post-init, module repo test fixtures, PR checks):

```rego
package policy.provider.allowlist

import rego.v1

# input: .terraform.lock.hcl via conftest's HCL parser
# shape: {"provider": {"<addr>": [{"version": "...", "hashes": [...]}]}}

deny_unknown_provider contains msg if {
	some addr in object.keys(input.provider)
	not data.providers[addr]
	msg := sprintf("PROV001: provider %q is not on the mirror allowlist", [addr])
}

deny_unapproved_version contains msg if {
	some addr, blocks in input.provider
	allowed := data.providers[addr]
	some b in blocks
	not b.version in allowed
	msg := sprintf("PROV002: %s %s is not an approved version", [addr, b.version])
}

deny_missing_h1 contains msg if {
	some addr, blocks in input.provider
	some b in blocks
	count([h | some h in b.hashes; startswith(h, "h1:")]) == 0
	msg := sprintf("PROV003: %s %s has no h1: hash — mirror install will fail", [addr, b.version])
}
```

**Plan-JSON policy** (Atlantis policy stage, catches providers before/without
lock context):

```rego
package policy.provider.allowlist_plan

import rego.v1

# input: terraform show -json output

deny contains msg if {
	some _, pc in input.configuration.provider_config
	not data.providers[pc.full_name]
	msg := sprintf("PROV001: provider %q is not on the mirror allowlist", [pc.full_name])
}
```

**required_providers policy** (module repos, pre-consumption — flags a module
that could never resolve against the mirror):

```rego
package policy.provider.allowlist_src

import rego.v1

canonical(src) := src if count(split(src, "/")) == 3
canonical(src) := sprintf("registry.terraform.io/%s", [src]) if count(split(src, "/")) == 2

deny contains msg if {
	some t in input.terraform
	some rp in t.required_providers
	some name, cfg in rp
	not data.providers[canonical(cfg.source)]
	msg := sprintf("PROV001: required provider %q (%s) is not on the mirror allowlist", [name, cfg.source])
}
```

Usage is the same invocation everywhere, differing only in input: Atlantis's
policy stage runs the plan-JSON and lock-file rules from the pulled bundle;
module repo CI and pre-Atlantis PR checks run `conftest test` over `*.tf` and
fixture lock files against the identical bundle (`--policy oci://…`). Because
the data document is `sluice export` output from the manifest in this same repo,
a yank merge rebuilds bundle and mirror from one diff — there is no second list
to update. The rego itself is never generated: rego's whole design separates
logic from data, so the three rules above are written once and tested, while
every merge only regenerates the data they consume. Generating rules from HCL
would add codegen and review noise for zero enforcement gain.

## API / Interface Changes

New repo and OCI artifact family. The bundle (policies + data + metadata,
signed) is the only contract consumers see; the stable violation codes are the
contract with dashboards and the exceptions service.

## Data Model

Generated JSON data documents keyed by domain; `data/exceptions.json` entries
carry scope (repo/resource), code, expiry, and approval reference so
annotated-pass behavior is data-driven.

## Testing Strategy

Covered per-component above; end-to-end acceptance is the contract test plus a
canary repo in Atlantis running the `stable` bundle against known-good and
known-violating fixtures on a schedule.

## Migration / Rollout Plan

1. Repo + conventions + generator; hand-write the provider-governance and
   lock-file policies from the mirror rollout.
2. OCI publish + signing; Atlantis switches from repo-local policies to the
   pinned bundle.
3. repo-guardian integration; reusable CI workflow.
4. Module-source policies in warn; exceptions data wiring; promotions per
   cohort.

## Open Questions

None currently open. Resolved (2026-07-31):

- **Bundle registry**: OCI is the commitment; the concrete registry is deferred
  (likely ECR or GitHub Packages). The design deliverable is a per-registry
  setup guide capturing the expectations either must meet to work with the
  sluice → bundle pipeline (see Distribution) — so whichever is picked, the
  contract is already written down.
- **Bundle granularity**: one org bundle. A single pin per consumer, and
  cross-domain consistency is atomic — the provider data document and the
  policies that consume it can never skew. Split per-domain only when a consumer
  demonstrably needs a different release cadence.
- **Generator scope**: the generator emits data documents and docs only — no
  Rego, ever. The rules are the same small shape at every enforcement point
  (parse the input conftest already understands — plan JSON, lock-file HCL,
  `*.tf` — then membership-check against `data.*`, pass or fail), so they are
  hand-written once per domain and owned as source. No generated-rule category
  to police, a single rule provenance for reviewers, and ADR-0001 holds without
  qualification.
- **Bundle version floor**: yes, repo-guardian enforces it — `warn_*` first,
  floor published in bundle metadata (e.g. `minimum_supported`). Stale policy
  pins are precisely the silent drift that unwinds enforcement. Promote to deny
  only after Renovate's OCI bumps have a track record.

## References

- RFC: Infrastructure Compliance via Policy-as-Code
- RFC: Terraform Provider Cache and Internal Provider Mirror (first data source)
- DESIGN: Provider Mirror CLI (approved-providers manifest)
- Conftest OCI sharing — conftest.dev; OPA metadata annotations —
  openpolicyagent.org
