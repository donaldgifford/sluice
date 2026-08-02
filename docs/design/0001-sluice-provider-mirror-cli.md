---
id: DESIGN-0001
title: "sluice — Provider Mirror CLI"
status: Draft
author: Donald Gifford
created: 2026-07-27
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0001: sluice — Provider Mirror CLI

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Commands](#commands)
  - [Desired state (HCL)](#desired-state-hcl)
  - [Reconciliation](#reconciliation)
  - [Ingest pipeline (per added version)](#ingest-pipeline-per-added-version)
  - [Publish ordering](#publish-ordering)
  - [Removal semantics](#removal-semantics)
  - [Observability](#observability)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

**Status:** Draft **Author:** Donald **Date:** 2026-07-26

## Overview

`sluice` is a Go CLI that reconciles a declarative, HCL-authored set of approved
Terraform provider versions against an S3-backed provider network mirror. It
follows plan/apply semantics: `plan` diffs desired state against the mirror's
protocol index files, `apply` downloads from the origin registry, verifies
signatures and checksums, and publishes the protocol artifacts to S3.

## Goals and Non-Goals

### Goals

- Declarative desired state in HCL (directory of files or a single file), no
  YAML.
- No state file: the mirror's own `index.json` files are the actual state.
- Cryptographic verification at ingest — a version that fails verification
  cannot be published.
- Deterministic, reviewable diffs; CI-first ergonomics (`-detailed-exitcode`,
  `-json`).
- Deliberate, auditable removal of versions from service (yank workflow).

### Non-Goals

- Serving the mirror (S3 does this; see the bucket module design).
- Mirroring Terraform modules, the Terraform binary, or other artifacts.
- Implementing the provider _registry_ protocol — this is a network-mirror
  publisher only.
- Multi-writer coordination beyond conditional writes (CI is the single writer).
- Anything outside the bucket: the Terragrunt provider cache is a separate
  system and another tool's implementation detail — sluice never reads or writes
  it.

## Background

Spawned by RFC "Terraform Provider Cache and Internal Provider Mirror". The
network mirror protocol is static JSON + zips; `terraform providers mirror` was
rejected as the engine because it resolves version constraints from generated
configs rather than publishing an exact declared set. HCL parsing and decoding
builds on hclkit.

## Detailed Design

### Commands

- `validate` — parse and schema-check the HCL; verify provider addresses are
  well-formed source addresses.
- `plan` — fetch each declared provider's `index.json` and `<version>.json`
  files from S3, diff against desired state, print human diff or `-json`. Exit 0
  (no changes), 2 (changes), 1 (error).
- `apply` — execute the plan: publish additions, retract removals.
  `-auto-approve` for CI; interactive confirm otherwise.
- `export` — emit the canonical JSON projection of desired state
  (`{"providers": {"<addr>": ["<version>", ...]}}`). No network, no side
  effects; deterministic output. The policy repo commits this as its allowlist
  data document, so policy data and mirror content derive from one parser.
- `bootstrap` — scan one or more paths for `.terraform.lock.hcl` files and emit
  seed HCL covering every provider/version currently in use. Used once during
  rollout.

### Desired state (HCL)

```hcl
mirror {
  bucket    = "org-tf-mirror"
  region    = "us-east-1"
  platforms = ["linux_amd64", "darwin_arm64"]
}

provider "registry.terraform.io/hashicorp/aws" {
  versions  = ["6.2.0", "6.3.0"]
  platforms = ["linux_amd64"] # optional override of mirror.platforms
}
```

Block label is the full provider source address (`hostname/namespace/type`),
mapping 1:1 onto the mirror path layout and supporting non-hashicorp namespaces
and other registries without special cases. Files in a directory are merged as
standard HCL bodies; duplicate provider addresses across files are an error.

### Reconciliation

Desired: the set of (provider, version, platform) tuples from HCL. Actual:
parsed from the bucket's `index.json` and `<version>.json` files. Diff produces
three action types: `add-version`, `remove-version`, `add-platform` (a new
platform for an already-mirrored version). Nothing else is ever computed — the
tool never "upgrades" or resolves constraints.

### Ingest pipeline (per added version)

1. Registry API: resolve download metadata per platform (URL, filename,
   `SHA256SUMS`, `SHA256SUMS.sig`, signing keys).
2. Verify the GPG signature over `SHA256SUMS` against the registry-published
   signing keys (ProtonMail/go-crypto openpgp).
3. Download the zip; verify its SHA-256 against the signed sums file.
4. Compute the mirror hash with `dirhash.HashZip`
   (`golang.org/x/mod/sumdb/dirhash`) — the same `h1:` scheme Terraform
   validates.
5. Stage the `<version>.json` entry (platform → archive path + `h1:` hash).
6. Sign: `cosign sign-blob` over the zip (keyless via CI OIDC or KMS), uploading
   the signature and an in-toto attestation (provider, version, platform,
   SHA-256, `h1:`, upstream signing key ID, authorizing commit) alongside the
   artifact.

Any verification failure aborts the entire apply with a non-zero exit and no
partial publish for that provider.

### Publish ordering

Per provider: zips first, then `<version>.json` files, then `index.json` last.
The index write is the atomic publish and uses an S3 conditional write (ETag
precondition captured at plan time). A precondition failure means concurrent
modification: the apply aborts and instructs a re-plan.

### Removal semantics

`remove-version` rewrites `index.json` without the version and deletes the
`<version>.json`. Zips are left in place — bucket versioning and the deny-delete
policy retain them for forensics. Removal is only ever driven by an HCL change,
making yanks reviewable one-line PRs. A yank controls new installs only;
immediate usage enforcement is the policy layer (lock-file allowlist generated
from this same manifest), and cache convergence belongs to the Atlantis
deployment — invalidate and rewarm from the mirror — not to sluice.

### Observability

Structured log (JSON) of every published or retracted artifact: provider
address, version, platform, `h1:`, SHA-256, registry key ID that signed the
sums, S3 object version ID. This log is the ingest audit trail alongside
CloudTrail.

## API / Interface Changes

New CLI only. Global flags: `-config-dir` / `-config-file` (mutually exclusive),
`-json`, `-detailed-exitcode` (plan), `-auto-approve` (apply). `-json` plan
output schema: `{ "add": [...], "remove": [...], "add_platform": [...] }` with
full tuples, stable field names for the PR-comment bot.

## Data Model

Desired: HCL schema above. Actual: the network mirror protocol files —
`index.json` (`{"versions": {"6.3.0": {}}}`) and `<version>.json`
(`{"archives": {"linux_amd64": {"url": "...zip", "hashes": ["h1:..."]}}}`). No
other persisted state.

## Testing Strategy

- Unit: HCL decode/validation, diff engine (table-driven), version.json
  construction, h1 golden values against known provider releases.
- Integration: fake registry via `httptest` serving fixture zips/sums/keys
  (including tampered fixtures asserting verification failure); S3 via
  LocalStack container.
- End-to-end (CI): apply against LocalStack, then run `terraform init` in a
  container with an exclusive `network_mirror` config pointed at it — the real
  consumer is the test oracle.
- Standards per the internal Go style guidance; race detector on all packages.

## Migration / Rollout Plan

1. `bootstrap` against checked-out repos to seed the approved-versions HCL.
2. Apply to the mirror bucket; validate with a canary repo before the Atlantis
   cutover (RFC Phase 3).
3. Hand operation to CI (see the CI pipelines design doc); humans stop running
   `apply` locally.

## Open Questions

None currently open. Resolved:

- **Per-provider `platforms` overrides** (2026-07-31): kept. The diff engine
  already models per-version platform sets (`add-platform` exists regardless),
  and the CI-only provider case is concrete — a provider pinned to `linux_amd64`
  skips mirroring darwin artifacts nobody installs.
- **OpenTofu**: supported day one; an e2e canary provider from
  `registry.opentofu.org` pins the API surface.
- **Per-artifact signing**: every published artifact gets a cosign signature +
  in-toto attestation (ingest pipeline step 6).

## References

- RFC: Terraform Provider Cache and Internal Provider Mirror
- Provider network mirror protocol —
  developer.hashicorp.com/terraform/internals/provider-network-mirror-protocol
- hclkit; libtftest (LocalStack patterns)
- DESIGN: Provider Mirror Bucket Terraform Module; DESIGN: Provider Mirror CI
  Pipelines
