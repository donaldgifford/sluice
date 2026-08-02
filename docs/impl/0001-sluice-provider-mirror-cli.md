---
id: IMPL-0001
title: "sluice Provider Mirror CLI"
status: Draft
author: Donald Gifford
created: 2026-08-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0001: sluice Provider Mirror CLI

**Status:** Draft **Author:** Donald Gifford **Date:** 2026-08-01

<!--toc:start-->

- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Manifest core — config schema, validation, validate](#phase-1-manifest-core--config-schema-validation-validate)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Diff engine and offline projections — export, bootstrap](#phase-2-diff-engine-and-offline-projections--export-bootstrap)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Registry client and cryptographic verification](#phase-3-registry-client-and-cryptographic-verification)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Publisher — plan and apply](#phase-4-publisher--plan-and-apply)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Integration, e2e, and hardening](#phase-5-integration-e2e-and-hardening)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement the `sluice` CLI from DESIGN-0001: a Go binary that reconciles a
declarative HCL manifest of approved Terraform provider versions against an
S3-backed network mirror, with plan/apply semantics and cryptographic
verification at ingest.

**Implements:** DESIGN-0001 (program context: `docs/roadmap.md`, workstream 1)

## Scope

### In Scope

- The five commands: `validate`, `plan`, `apply`, `export`, `bootstrap`.
- The full ingest verification chain: registry metadata → GPG over `SHA256SUMS`
  → zip SHA-256 → `h1:` via dirhash → cosign signature + in-toto attestation.
- Publish ordering with ETag-conditional index writes, removal semantics,
  idempotent retry, and the four-value exit-code contract (0/1/2/3).
- The structured JSON audit log for every publish/retract.
- Unit, integration (LocalStack), and e2e (`init` oracle) test suites wired into
  this repo's CI.

### Out of Scope

- The bucket itself and its IAM (DESIGN-0002; the CLI consumes `bucket` +
  `region` from HCL and the default AWS credential chain).
- Operational workflows — plan-on-PR, apply-on-merge, drift backstop, Renovate
  (DESIGN-0003; tracked from the roadmap).
- The mirror verifier (runtime shape and code home under INV-0001) and the
  composite plan-comment action — separate deliverables even if they land in
  this repo.
- The policy library and `data/providers.json` consumption (DESIGN-0004); this
  doc only guarantees `export`'s byte-stable contract.
- Distribution channel work (internal tool registry).

## Implementation Phases

Phases are sequential; each builds on the previous. A phase is complete when all
its tasks are checked off and its success criteria are met.

---

### Phase 1: Manifest core — config schema, validation, `validate`

The pure foundation: HCL in, validated model out. No network, no AWS.
Establishes the command dispatch and exit-code discipline everything else plugs
into.

#### Tasks

- [x] Add `hclkit` v0.1.0 and `cobra` — the first entries in `go.mod`
      (`go mod tidy`).
- [x] `internal/config` schema types: `mirror` block (`bucket`, `region`,
      `platforms`) and `provider` blocks (label = full source address,
      `versions`, optional `platforms` override).
- [x] Loader: `--config-dir` via hclkit `LoadDir` with `MergeAppend` (top-level
      `*.hcl` merged as HCL bodies; an empty directory errors for free) and
      `--config-file` via `LoadFile`; the two flags mutually exclusive with a
      clear usage error.
- [x] Merge semantics: duplicate provider labels across files are an error —
      merge is explicit, never a silent union.
- [x] Validation rules, each with a targeted error message:
  - [x] exactly one `mirror` block across all files;
  - [x] labels parse as three-segment source addresses
        (`hostname/namespace/type`) with a valid hostname;
  - [x] versions are exact semver (`hashicorp/go-version`), unique per block;
        constraint syntax (`~>`, `>=`, ...) rejected with an explanation of why
        sluice never resolves;
  - [x] platforms drawn from the curated `os_arch` matrix (`linux_amd64`,
        `darwin_arm64`, ...), one place in code to extend;
  - [x] empty `versions` list is an error (removing a provider = deleting its
        block).
- [x] cobra command tree: root plus the five subcommands, pflag double-dash flag
      surface (`--config-dir`, `--auto-approve`, ...); update the spec's
      single-dash flag examples to match; `validate` wired end to end with exit
      0/1.
- [x] Table-driven tests: every validation rule has at least one passing and one
      failing case; error text asserted.

#### Success Criteria

- `sluice validate` exits 0 on every valid fixture and 1 with a single
  actionable error line per failure class on invalid fixtures.
- Every validation rule appears in the test tables.
- `just ci` green, including the `internal/` coverage gate.

---

### Phase 2: Diff engine and offline projections — `export`, `bootstrap`

The reconciliation brain and the two commands that need no network. `export` is
the policy repo's data contract, so its bytes are frozen here.

#### Tasks

- [x] `internal/mirror`: network-mirror protocol types (`index.json`,
      `<version>.json`) and desired-state expansion into (provider, version,
      platform) tuples, honoring per-provider `platforms` overrides.
- [x] Diff engine: pure struct-in/struct-out producing exactly `add-version`,
      `remove-version`, `add-platform`; deterministic action ordering so output
      is stable.
- [x] Table-driven diff tests: each action type, combinations, the empty plan,
      platform overrides, and full-provider removal (deleted block → all
      versions removed).
- [x] `sluice export`: canonical JSON projection
      (`{"providers": {"<addr>": ["<version>", ...]}}`) — sorted keys, sorted
      versions, deterministic bytes; golden tests; no network, no side effects.
- [x] `sluice bootstrap PATH... [--out FILE]`: walk for `.terraform.lock.hcl`,
      collect provider/version pairs, dedupe, group by namespace, sort;
      constraint info ignored (lock files record exact versions). Manifest bytes
      emitted via `hcl/v2/hclwrite` — hclkit v0.1.0 has no write path.
- [x] Bootstrap fixture tree: nested directories, repeated providers across
      repos, a lock file with multiple providers.
- [x] Round-trip test: `bootstrap` output piped into `validate` exits 0.

#### Success Criteria

- `export` output is byte-identical across repeated runs and platforms (golden
  test enforces it).
- `bootstrap` → `validate` round-trip passes on the fixture tree.
- Diff tables cover all three action types and the empty plan; identical
  desired/actual states always produce an empty diff.

---

### Phase 3: Registry client and cryptographic verification

The trust boundary. Everything entering the mirror is verified here, and the
tampered-fixture suite is the proof it fails closed.

#### Tasks

- [x] `internal/registry`: resolve per-platform download metadata from the
      origin registry API
      (`/v1/providers/{ns}/{type}/{version}/download/{os}/{arch}`): zip URL,
      filename, `SHA256SUMS`, `SHA256SUMS.sig`, publisher signing keys —
      protocol-generic so `registry.opentofu.org` addresses work identically.
- [x] GPG signature verification over `SHA256SUMS` against the
      registry-published keys (`ProtonMail/go-crypto` openpgp).
- [ ] Zip download (streamed to a temp file, size-bounded, context-aware) and
      SHA-256 verification against the signed sums entry.
- [ ] Retry with backoff on registry calls; clean context cancellation.
- [x] `internal/hash`: `h1:` via `dirhash.HashZip`
      (`golang.org/x/mod/sumdb/dirhash`); golden tests whose expected values are
      cross-checked against real `.terraform.lock.hcl` entries for the fixture
      providers.
- [ ] `httptest` fake registry serving fixture zips, sums, sigs, and keys,
      including an OpenTofu-registry-shaped fixture.
- [ ] Tampered-fixture suite — each case must abort with a wrapped error
      carrying the (provider, version, platform) tuple and stage nothing:
  - [ ] `SHA256SUMS.sig` invalid for the sums file;
  - [ ] sums signed by a key the registry did not publish;
  - [ ] zip modified after signing (checksum mismatch);
  - [ ] zip's entry missing from the sums file.

#### Success Criteria

- Every tampered fixture exits 1 with zero staged artifacts.
- Golden `h1:` values match published lock-file hashes for the fixture
  providers.
- Unit tests make no real network calls.

---

### Phase 4: Publisher — `plan` and `apply`

The write path: ordered, atomic at the index, conditional, idempotent, signed,
and audited.

#### Tasks

- [ ] `internal/publish`: S3 behind a small interface; read actual state from
      the bucket's `index.json` and `<version>.json` files; a missing index
      means an unmirrored provider (first publish), not an error.
- [ ] `sluice plan`: human diff output; `--json` with the stable `add` /
      `remove` / `add_platform` schema (golden test — this is the comment-bot
      contract); `--detailed-exitcode` (0 clean / 1 error / 2 changes); capture
      the index ETag for apply.
- [ ] `sluice apply`: interactive confirmation or `--auto-approve`; staging of
      verified artifacts; publish ordering per provider — zips →
      `<version>.json` → `index.json` last.
- [ ] ETag-conditional `index.json` write; precondition failure aborts with exit
      3 and a message instructing a re-plan.
- [ ] Removal semantics: rewrite `index.json` without the version, delete
      `<version>.json`, leave zips in place.
- [ ] Idempotent retry: a rerun after an induced mid-apply failure republishes
      staged artifacts and converges.
- [ ] Per-artifact signing: `cosign sign-blob` (keyless via CI OIDC or KMS) plus
      in-toto attestation (provider, version, platform, SHA-256, `h1:`, upstream
      signing key ID, authorizing commit) uploaded alongside the artifact — by
      shelling out to the mise-pinned cosign binary; a missing or wrong-version
      cosign fails closed.
- [ ] Add cosign to `mise.toml` (with a `# renovate:` annotation) and CI.
- [ ] Structured `slog` JSON audit line per publish/retract (action, provider,
      version, platform, `h1:`, `sha256`, `signing_key_id`, `s3_version_id`);
      schema asserted in tests.
- [ ] Exit-code contract implemented exactly: 0 success/no changes, 1 error, 2
      plan changes with `--detailed-exitcode`, 3 conditional-write conflict.

#### Success Criteria

- Against LocalStack: a full apply followed by an immediate re-plan yields an
  empty plan (determinism).
- A concurrent-writer test observes exit 3 and an intact, uncorrupted index.
- A rerun after induced mid-apply failure converges to the desired state.
- Every published artifact has its signature and attestation objects alongside
  it; every publish/retract emitted exactly one audit line.

---

### Phase 5: Integration, e2e, and hardening

The real consumer becomes the test oracle, and the tool gets release-ready.

#### Tasks

- [ ] LocalStack integration suite behind `//go:build integration`, run via
      `just test-integration` and a CI service container.
- [ ] e2e CI job: `apply` to LocalStack, then `init` in a container with an
      exclusive `network_mirror` block pointed at it — matrixed over both
      `terraform init` and `tofu init`; canaries include one
      `registry.opentofu.org` provider.
- [ ] Error-message audit: every error path actionable, wrapped with `%w`,
      carrying the (provider, version, platform) tuple where applicable;
      `errors.Is`/`errors.As` handling at the top of `cmd/sluice`.
- [ ] `doc.go` for every `internal/` package; SPDX headers throughout.
- [ ] `--version`-style output verified (`version`, `commit`, `date` from
      ldflags).
- [ ] Race detector clean across all packages; `internal/` coverage at or above
      the gate.
- [ ] README quickstart updated with real command examples; cross-check
      `docs/sluice-spec.md` against the implementation (flags, exit codes,
      output formats) and fix any drift.

#### Success Criteria

- e2e job green in CI: a fresh `init` installs the canary providers entirely
  from the sluice-populated mirror.
- `just ci` fully green (lint, race tests, coverage gate, build, govulncheck,
  license check, changelog check).
- Spec and implementation agree — no documented flag, exit code, or output
  format differs from behavior.

---

## File Changes

| File                       | Action | Description                         |
| -------------------------- | ------ | ----------------------------------- |
| `internal/config/`         | Create | HCL schema, loader, validation      |
| `internal/mirror/`         | Create | Protocol types, desired state, diff |
| `internal/registry/`       | Create | Origin client, GPG + SHA-256 verify |
| `internal/hash/`           | Create | `h1:` via dirhash (golden-tested)   |
| `internal/publish/`        | Create | S3 publisher, ordering, retract     |
| `internal/bootstrap/`      | Create | Lock-file walker → seed HCL         |
| `cmd/sluice/`              | Modify | Command dispatch, flags, exit codes |
| `go.mod` / `go.sum`        | Modify | First dependencies                  |
| `mise.toml`                | Modify | Add cosign (renovate-annotated)     |
| `.github/workflows/ci.yml` | Modify | Integration + e2e jobs              |
| `README.md`                | Modify | Real quickstart examples            |

## Testing Plan

- [ ] Unit: table-driven throughout; every validation rule, every diff action
      type, `export` golden bytes, `-json` plan schema golden, `h1:` golden
      values.
- [ ] Verification: the tampered-fixture suite — the load-bearing test class;
      every check provably fails closed.
- [ ] Integration: `httptest` fake registry; LocalStack S3 behind the
      `integration` build tag.
- [ ] e2e: consumer `init` (terraform and tofu matrix) against a
      sluice-populated mirror.
- [ ] Race detector on all suites; `internal/` coverage gate enforced in CI.

## Dependencies

- `hclkit` v0.1.0 (released API checked 2026-08-01): `LoadFile`/`LoadDir` with
  `MergeAppend` cover the loader exactly — merged HCL bodies across top-level
  `*.hcl` files, position-aware diagnostics, empty-directory error built in. Two
  gaps, neither blocking: the decode-time validator subpackage its docs plan is
  not in this release (sluice's validation pass is its own code regardless), and
  there is no write path (`bootstrap` uses `hcl/v2/hclwrite` directly, already
  in the dependency tree). It also bundles no version library, so
  `hashicorp/go-version` is a direct dependency here.
- `cobra` (command tree), `ProtonMail/go-crypto` (openpgp), `golang.org/x/mod`
  (dirhash), `hashicorp/go-version` (version parsing/sorting), `aws-sdk-go-v2`
  (S3).
- cosign binary (Phase 4 task adds it to `mise.toml` and CI).
- LocalStack for integration/e2e; terraform and tofu container images for the
  oracle.
- Nothing at the roadmap level blocks this workstream — Phases 1–5 are
  executable regardless of INV-0001's outcome.

## Open Questions

None currently open. Resolved (2026-08-01):

- **Command-line framework**: cobra — the house standard, for better or worse,
  and what hclkit's own CLI already uses. Accepted consequence: the flag surface
  is pflag's double-dash spelling (`--config-dir`, `--detailed-exitcode`,
  `--auto-approve`), diverging from the single-dash examples in DESIGN-0001 and
  the spec; those examples get updated as part of Phase 1's dispatch task, and
  the Phase 5 cross-check catches any stragglers.
- **cosign integration**: shell out to the mise-pinned binary (keyless via CI
  OIDC or KMS); sluice fails closed if cosign is missing or the wrong version.
  No sigstore library dependency.
- **e2e consumer oracle**: matrix — both `terraform init` and `tofu init`
  against the same LocalStack mirror.
- **Semver library**: `hashicorp/go-version`, matching Terraform's own version
  semantics for parsing, prereleases, and the sort order inside `export`.
  Checked against hclkit v0.1.0: it bundles no version library, so this is a new
  direct dependency (details under Dependencies).

## References

- DESIGN-0001 sluice — Provider Mirror CLI (all design-level questions resolved)
- `docs/roadmap.md` — program sequencing and cross-repo context
- `docs/sluice-spec.md` — working spec: HCL schema, command surface, exit codes,
  `-json` schemas, yank runbook
- RFC-0001 Terraform Provider Cache and Internal Provider Mirror
- Provider network mirror protocol —
  developer.hashicorp.com/terraform/internals/provider-network-mirror-protocol
