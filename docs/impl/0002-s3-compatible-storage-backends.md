---
id: IMPL-0002
title: "S3-Compatible Storage Backends"
status: In Progress
author: Donald Gifford
created: 2026-09-15
---

<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL-0002: S3-Compatible Storage Backends

**Status:** In Progress **Author:** Donald Gifford **Date:** 2026-09-15

<!--toc:start-->

- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Endpoint configuration and client wiring](#phase-1-endpoint-configuration-and-client-wiring)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Capability probe and degraded modes](#phase-2-capability-probe-and-degraded-modes)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Test matrix, serving recipe, and rollout](#phase-3-test-matrix-serving-recipe-and-rollout)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement DESIGN-0005: run `sluice` plan/apply against S3-compatible storage
(Garage as reference) with explicit endpoint configuration, a startup capability
probe, and fail-closed degraded modes where the backend cannot deliver AWS
semantics. DESIGN-0005's open questions are resolved all-(a) (2026-09-16); this
plan implements those resolutions. Phase 1 is independent of them.

**Implements:** DESIGN-0005 (program context: `docs/roadmap.md`; storage
alternative to workstream 1's AWS path and workstream 2's bucket module)

## Scope

### In Scope

- `mirror` block `endpoint` / `path_style` attributes with HCL validation, plus
  `SLUICE_S3_ENDPOINT` / `SLUICE_S3_PATH_STYLE` env overrides.
- Production client wiring: `BaseEndpoint` + `UsePathStyle`, checksums disabled
  (`RequestChecksumCalculation` / `ResponseChecksumValidation` only when
  required), STS-free credential chain.
- Capability probe (`GetBucketVersioning` + scratch-key conditional round-trip
  under `_sluice/probe/`), full vs degraded mode selection, fail-closed `apply`
  with `--allow-unversioned-backend` opt-in and audit-trail warning.
- Empty `version_id` audit refs and permanent-delete yank semantics in degraded
  mode; `_retired/` recycle-bin copy-before-delete.
- Degraded-profile test suite, generalized `SLUICE_S3_ENDPOINT` seam, Garage e2e
  leg, Caddy serving recipe docs.

### Out of Scope

- Versioning, Object Lock, KMS, or bucket-policy emulation on backends that lack
  them (honest degradation instead).
- The Garage cluster itself, bucket/key provisioning, and the reverse proxy
  deployment (operator-owned; documented, not built).
- Multi-backend failover; SigV2-only stores; changes to the AWS path when
  `endpoint` is null (must stay byte-identical).

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its tasks
are checked off and its success criteria are met.

---

### Phase 1: Endpoint configuration and client wiring

HCL surface, env overrides, validation, and the production client. No behavior
change when `endpoint` is null.

#### Tasks

- [x] Add `endpoint` (optional string) and `path_style` (optional bool, default
      false) to the `mirror` HCL block (`internal/config/load.go`); `endpoint`
      must parse as an http(s) URL, `path_style` required true when `endpoint`
      is set.
- [x] Plumb through `Mirror` model (`internal/config/config.go`) with validation
      errors matching the existing `Missing required argument` style;
      table-driven tests in `load_test.go` / `validate_test.go`.
- [x] Add `SLUICE_S3_ENDPOINT` / `SLUICE_S3_PATH_STYLE` overrides with
      explicit-flags-beat-env-beats-HCL precedence (plus `--s3-endpoint` /
      `--s3-path-style` flags as the explicit tier).
- [x] Extend `newBucket` (`cmd/sluice/bucket.go`): endpoint set →
      `BaseEndpoint` + `UsePathStyle`, checksums to when-required only, static
      credential chain, no STS/account/metadata calls; refuse non-TLS endpoints
      except loopback.
- [ ] Update `docs/sluice-spec.md` HCL schema for the two new attributes.

#### Success Criteria

- `plan`/`validate` against LocalStack via HCL `endpoint` (not just the test
  seam) succeed; AWS path with null `endpoint` is behavior-identical.
- `just lint` and `just test` green; spec documents the new surface.

---

### Phase 2: Capability probe and degraded modes

Probe, mode selection, fail-closed apply, and degraded audit semantics.

#### Tasks

- [ ] Implement the startup probe (`GetBucketVersioning` + scratch-key
      `If-None-Match` / wrong-`If-Match` round-trip + cleanup under
      `_sluice/probe/`); run before any read, print mode in `plan` verbose
      output.
- [ ] Fail-closed `apply`: refuse degraded backends pre-mutation without
      `--allow-unversioned-backend`, error naming the missing capability;
      degraded applies log the weakening to the audit trail.
- [ ] Degraded audit refs: empty `version_id` fields; document permanent yank.
- [ ] `_retired/` recycle-bin copy-before-delete on yank in degraded mode
      (retention-managed prefix).
- [ ] Unit tests with a stubbed `Bucket` for probe outcomes, fail-closed
      refusal, and opt-in apply paths.

#### Success Criteria

- Against Garage: probe reports degraded; `apply` without the flag exits 1
  pre-mutation; with the flag it completes with warned audit entries.
- Against AWS/LocalStack: full mode, zero behavior change, conditional-write
  races still surface as `ErrConflict`.

---

### Phase 3: Test matrix, serving recipe, and rollout

Prove the whole chain and hand operators the runbook.

#### Tasks

- [ ] Generalize the integration seam to `SLUICE_S3_ENDPOINT` (keep LocalStack
      as the full-mode fixture); add the degraded-profile suite runnable against
      Garage.
- [ ] Garage e2e leg: apply to Garage, `terraform init` through the
      website-endpoint proxy as the sole provider source (origin taken from the
      mirror-bucket module's `mirror_url`, override for Garage).
- [ ] Document the Caddy serving recipe (path→`Host` mapping, TLS, IP allowlist)
      and the worked Garage setup (bucket, keys, grants, manifest) in the ops
      runbook.
- [ ] Sandbox canary: one consumer on the Garage mirror with a green week of
      drift backstop before any fleet move.

#### Success Criteria

- `just test-integration` green against both profiles; e2e init oracle green
  against the Garage serving origin.
- Runbook reviewed; canary consumer installing providers exclusively from
  Garage.

---

## File Changes

| File                         | Action | Description                                      |
| ---------------------------- | ------ | ------------------------------------------------ |
| `internal/config/load.go`    | Modify | `endpoint` / `path_style` HCL attributes         |
| `internal/config/config.go`  | Modify | `Mirror` model fields                            |
| `internal/config/*_test.go`  | Modify | Validation table tests                           |
| `cmd/sluice/bucket.go`       | Modify | Endpoint/path-style/checksum wiring, TLS refusal |
| `internal/publish/probe.go`  | Create | Capability probe, mode selection                 |
| `internal/publish/apply.go`  | Modify | Fail-closed degraded gate, audit warning         |
| `internal/publish/*_test.go` | Modify | Probe and degraded-path unit tests               |
| `docs/sluice-spec.md`        | Modify | HCL schema for new attributes                    |

## Testing Plan

- [ ] Unit tests for HCL validation, probe outcome matrix, and fail-closed /
      opt-in apply paths (stubbed `Bucket`).
- [ ] Integration suite green on LocalStack (full) and Garage (degraded) via the
      generalized endpoint seam.
- [ ] E2E init oracle green against the Garage serving origin.
- [ ] Coverage floor holds (`just coverage-gate`); `just ci` green.
- [ ] Scope holds: every leg runs without an AWS sandbox or VPCE (plan suites,
      LocalStack, Garage); AWS live evaluation stays deferred.

## Dependencies

- Garage sandbox (bucket + publisher/reader keys) for the degraded profile and
  e2e leg.
- DESIGN-0005 open questions, resolved all-(a) 2026-09-16 (no longer blocking).
- Reverse-proxy host for the serving leg (operator-provided).

## Open Questions

None here — decided in DESIGN-0005's Open Questions (six items, resolved all-(a)
2026-09-16). This plan implements those resolutions.

## References

- DESIGN-0005 S3-Compatible Storage Backends (this plan's design)
- DESIGN-0001 sluice Provider Mirror CLI (`Bucket`/`Cond` contract); DESIGN-0003
  Provider Mirror CI Pipelines (concurrency, backstop); DESIGN-0002 Provider
  Mirror Bucket Terraform Module (AWS posture)
- `docs/sluice-spec.md` — HCL schema and exit codes
