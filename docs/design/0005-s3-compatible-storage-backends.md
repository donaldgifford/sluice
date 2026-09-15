---
id: DESIGN-0005
title: "S3-Compatible Storage Backends"
status: Draft
author: Donald Gifford
created: 2026-09-15
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0005: S3-Compatible Storage Backends

<!--toc:start-->

- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [CLI changes](#cli-changes)
  - [Degraded guarantees](#degraded-guarantees)
  - [Running with Atlantis](#running-with-atlantis)
  - [Backend conformance checklist](#backend-conformance-checklist)
  - [Using Garage with sluice](#using-garage-with-sluice)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

`sluice` speaks AWS S3 through aws-sdk-go-v2 with the default endpoint chain
(`cmd/sluice/bucket.go` — `LoadDefaultConfig` plus manifest region, no endpoint
override). This design adds named S3-compatible backends, with Garage as the
reference implementation, so self-hosters can run the mirror without AWS: an
explicit endpoint plus path-style addressing and static credentials on the
client side, a capability probe with fail-closed degraded modes where the
backend cannot deliver AWS semantics (no versioning, no conditional writes, no
bucket policy), and a serving story for Terraform/Atlantis reads off Garage's
website endpoint.

## Goals and Non-Goals

### Goals

- `plan`/`apply`/`validate` run unchanged in shape against Garage (and
  generically any SigV4 + path-style store) via explicit configuration.
- No silent guarantee loss: where the backend cannot deliver a semantic sluice
  depends on (conditional index writes, versioned audit refs, policy-enforced
  immutability), the CLI detects it and either refuses or requires an explicit
  opt-in — never quietly downgrades.
- Terraform/Atlantis consumers install providers from the Garage-backed mirror
  with no client-side changes beyond the `network_mirror` URL.
- A conformance checklist any S3-compatible provider can be held to, plus a
  worked Garage setup.

### Non-Goals

- Full AWS parity (versioning, Object Lock, KMS, bucket policy, CloudTrail
  events) on backends that do not have it — the design degrades honestly
  instead.
- Making Garage enforce AWS-style bucket policy — impossible; its permission
  model is per-key-per-bucket by design.
- Multi-backend failover or mirroring between backends.
- SigV2-only stores.

## Background

The publish path is already backend-agnostic by accident: `internal/publish`
programs to the `Bucket` interface (`Get`/`Put`/`List`/`Delete` plus the `Cond`
conditional-write type), and the LocalStack integration suite
(`internal/publish/integration_test.go`, `SLUICE_LOCALSTACK_ENDPOINT`) proves
the adapter works against an S3-compatible endpoint today with exactly two
client options — `BaseEndpoint` and `UsePathStyle`. What is missing is entirely
in the wiring: production construction (`cmd/sluice/bucket.go`) has no endpoint
override, no path-style switch, and no notion that versioning or preconditions
might be absent.

Garage's documented compatibility surface (verified September 2026 against the
Garage S3-compatibility reference) constrains the design:

- Implemented: SigV4 auth, path-style and vhost-style URLs,
  `Get/Put/Delete/ HeadObject`, `ListObjectsV2` (with pagination), multipart
  upload, `CopyObject`, `GetBucketLocation`, presigned URLs, SSE-C only.
- Missing: bucket versioning (`GetBucketVersioning` is a stub that always
  reports disabled), `PutObject` preconditions `If-Match` / `If-None-Match`
  (tracked upstream as duplicates of issue #1052 — preconditions are silently
  ignored, so a conditional write overwrites unconditionally), all AWS ACL and
  bucket-policy APIs (Garage permissions are per-access-key-per-bucket via its
  CLI/admin API), server-side bucket encryption configuration, Object Lock,
  lifecycle transitions (only expiration and MPU-abort are partially supported).
- Anonymous reads are not part of the S3 API surface: unauthenticated
  `GetObject` works only through the website endpoint (default port 3902,
  `garage bucket website --allow`), with per-bucket anonymous HTTP access
  tracked upstream (issue #263). The website endpoint resolves buckets by
  `Host`, not by path.
- The signing region is functionally arbitrary against Garage (any non-empty
  value signs correctly); it carries no routing meaning.

Two sluice internals collide directly with the missing pieces
(`internal/publish/apply.go`, `reader.go`, `bucket.go`): the single-writer
guarantee is `If-None-Match: *` on first publish and `If-Match: <index ETag>` on
update, mapped to S3 preconditions with 412/409 → `ErrConflict`; and `Put`
returns the object `VersionId` into the audit trail. Against Garage both
mechanisms evaporate: preconditions are accepted-and-ignored (the dangerous
shape — no error surfaces), and there are no versions to reference.

## Detailed Design

### CLI changes

- HCL: the `mirror` block gains two optional attributes — `endpoint` (string,
  null means AWS) and `path_style` (bool, default false). `region` stays
  required and doubles as the SigV4 signing region (any value works against
  Garage; keep the real region for AWS). Validation: `endpoint` must parse as an
  http(s) URL; `path_style = true` is required when `endpoint` is set unless the
  backend profile says otherwise (Garage mandates path-style — its docs tell
  every client to force it).
- Environment (12-factor override,mirroring the existing
  `SLUICE_LOCALSTACK_ENDPOINT` test seam): `SLUICE_S3_ENDPOINT`,
  `SLUICE_S3_PATH_STYLE`, with standard AWS credential env (`AWS_ACCESS_KEY_ID`
  / `AWS_SECRET_ACCESS_KEY`) carrying the Garage key pair. Explicit flags/env
  win over HCL; HCL wins over defaults. No new credential mechanism — Garage
  keys are SigV4 static keys by design.
- Wiring (`cmd/sluice/bucket.go`): when an endpoint is configured, construct
  with `BaseEndpoint` + `UsePathStyle` and static-chain credentials — the same
  three lines the LocalStack suite already exercises, promoted to production —
  plus checksum behavior matching the Terragrunt Garage profile
  (`skip_s3_checksum`): disable SDK request/response checksums
  (`RequestChecksumCalculation` / `ResponseChecksumValidation` set to only send
  when required), since partial S3 implementations choke on unexpected
  `x-amz-checksum-*` headers. No STS, account-ID, or metadata calls — Garage
  keys are not AWS IAM identities, so the client must be STS-free exactly as the
  Terragrunt profile's `skip_credentials_validation` /
  `skip_requesting_account_id` / `skip_metadata_api_check` require.
- Capability probe at startup (before any read): `GetBucketVersioning` plus a
  scratch-key conditional-write round-trip (`Put` with `If-None-Match`, then
  `Put` with a wrong `If-Match`, delete the key). The probe result selects the
  backend mode (see below) and is printed in `plan` verbose output so the mode
  is always reviewable. Probes use a `_sluice/probe/` prefix the reader ignores.

### Degraded guarantees

Backends sort into two modes. Full mode (AWS, and any compatible that versions
and honors preconditions) behaves exactly as today. Degraded mode (Garage as
documented above) changes the contract as follows, loudly:

- Conditional index writes are refused by default: `apply` exits non-zero before
  mutating anything when the probe reports preconditions unsupported, with an
  error naming the missing capability. `--allow-unversioned-backend` opts into
  degraded apply, and every degraded apply logs a warning to the audit trail.
  Rationale: on Garage the server answers 200 while ignoring the precondition,
  so proceeding by default would silently void the single-writer guarantee the
  whole pipeline design (DESIGN-0003 concurrency group included) assumes.
- The CI `concurrency` group becomes the _only_ writer serialization in degraded
  mode. The design therefore requires a single group across plan-on-main and
  apply (already the DESIGN-0003 shape) and forbids parallel applies —
  documented, not enforced in code.
- Audit `version_id` fields are empty in degraded mode (no versions exist). Yank
  (`Delete`) is permanent — nothing to retain for forensics. Operators wanting a
  forensic trail use the recycle-bin pattern (copy to a `_retired/` prefix
  before delete; see Open Questions 6) plus the cosign signatures/attestations,
  which still verify independently of storage.
- The DESIGN-0002 posture (VPCE-scoped anonymous reads, deny-delete,
  deny-policy-mutation) has no expression on Garage: no bucket policies exist.
  It is replaced by three controls — Garage per-key read/write grants (publisher
  key writes, reader keys read), network scoping of the S3 API endpoint, and the
  `DenyObjectDeletion` equivalent as operator discipline plus the drift backstop
  (below). The design records this as an accepted weakening with a narrower
  threat model, not as parity.

### Running with Atlantis

- CI jobs running `plan`/`apply` need a network route to the Garage S3 API
  endpoint and the publisher key pair (Vault/env-injected static keys — there is
  no OIDC-for-Garage equivalent of the AWS publisher role; the trust boundary
  moves from IAM to key custody plus the CI `concurrency` group).
- `validate` and PR `plan` are read-only as in DESIGN-0003; apply-on-merge is
  unchanged in shape, with the degraded-mode opt-in flag set explicitly in the
  workflow so the weakening is reviewable in git.
- The event-driven verifier path (CloudTrail → EventBridge → Lambda) does not
  exist against Garage. Integrity detection rests on the scheduled drift
  backstop (daily `plan`; exit 2 opens/updates the drift issue) — it works
  unchanged, since it only reads — plus the quarterly game-day, which should
  additionally cover "second writer wins silently" to prove the concurrency
  group holds.
- Terraform/Atlantis serving reads come from the Garage website endpoint (or a
  reverse proxy in front of it — recommended), not the S3 API endpoint:
  `.terraformrc` `provider_installation.network_mirror.url` points at the
  serving origin (see below). Atlantis runners need a route to the serving
  origin only; they never touch the S3 API endpoint or hold keys.
- Mirror contents are public upstream artifacts (provider zips, checksums,
  signatures) plus derived indexes — the same content the AWS design already
  serves to anonymous VPCE readers — so website-endpoint publicity is not a new
  exposure. The serving origin still gets IP allowlisting (or tailnet-only
  binding) at the proxy so the mirror is fleet-readable, not world-readable.

### Backend conformance checklist

A backend claims sluice support by satisfying, in order:

1. SigV4 request signing; path-style addressing (`host/bucket/key`).
2. `GetObject`, `PutObject`, `DeleteObject`, `HeadObject`, `HeadBucket`,
   `GetBucketLocation`, `ListObjectsV2` with `ContinuationToken` pagination.
3. ETag returned on single-part `PutObject` (content MD5 semantics, so
   `If-Match` values are meaningful where honored).
4. `If-Match` / `If-None-Match` on `PutObject` with 412/409 on violation (full
   mode), or explicit absence detected by the probe (degraded mode).
5. Versioned reads/writes with per-version identifiers (full mode audit trail),
   or explicit absence (degraded mode).
6. Tolerance of a checksum-free client: no required `x-amz-checksum-*` headers
   (the CLI disables request/response checksums against non-AWS endpoints, per
   the Terragrunt `skip_s3_checksum` precedent).
7. TLS on the configured endpoint (the CLI refuses `http://` outside explicitly
   loopback/test endpoints).

Conformance is demonstrated by pointing the existing integration suite at the
backend (generalize `SLUICE_LOCALSTACK_ENDPOINT` into the `SLUICE_S3_ENDPOINT`
override) and running the e2e init oracle (`terraform init` with the mirror as
the sole provider source) against the serving origin.

### Using Garage with sluice

Worked path (Garage v2.x):

1. `garage bucket create <mirror>`; `garage key create <publisher>` (+ a
   read-only key for CI plan jobs);
   `garage bucket allow --read --write <mirror> --key <publisher>`; read-only
   grant for the reader key.
2. `garage bucket website --allow <mirror>` (or the `anonymous_access` bucket
   setting once upstream lands it) and front the website endpoint with a reverse
   proxy that maps `https://mirror.internal/<key>` to the bucket's `Host`-routed
   origin and adds TLS plus IP allowlisting. Terraform's mirror client only ever
   issues `GET <prefix>/<path>` — any static origin works.
3. Manifest:
   `mirror { bucket = "<mirror>", region = "garage", endpoint = "https://s3.internal", path_style = true }`,
   keys in the environment, probe confirms degraded mode on first `plan`.
4. Apply workflow sets `--allow-unversioned-backend` explicitly; the single CI
   concurrency group is mandatory, not advisory.

## API / Interface Changes

- HCL `mirror` block: optional `endpoint`, optional `path_style` (new;
  validation as above). Everything else in `docs/sluice-spec.md` unchanged.
- Env: `SLUICE_S3_ENDPOINT`, `SLUICE_S3_PATH_STYLE` (new); credentials reuse the
  AWS chain.
- Flag: `--allow-unversioned-backend` on `apply` (new; required for degraded
  backends, inert on full backends).
- Exit codes unchanged; degraded-mode refusal surfaces as a pre-apply error
  (exit 1), never as a mid-apply conflict.

## Data Model

None. Object layout, index schema, and audit-log shape are backend-independent;
in degraded mode `version_id` audit fields are empty strings and yank leaves no
prior version behind.

## Testing Strategy

- Generalize the integration seam: `SLUICE_LOCALSTACK_ENDPOINT` becomes the
  generic `SLUICE_S3_ENDPOINT` override (LocalStack stays the full-mode
  fixture).
- Add a degraded-profile suite (runnable against Garage or a stub honoring the
  probe contract): probe detects missing versioning/preconditions; `apply`
  without the flag fails closed; with the flag it completes and the audit trail
  records the degraded warning with empty version refs.
- E2E init oracle gains a Garage leg: apply to Garage, `terraform init` through
  the website-endpoint proxy as the sole provider source.
- Publish the conformance checklist as the bar for adding any further backend.

## Migration / Rollout Plan

1. Land endpoint/path-style wiring + probe + fail-closed degraded mode behind
   the new HCL fields; AWS path byte-identical when `endpoint` is null.
2. Stand up Garage per the worked path in a sandbox; run the degraded suite and
   the init oracle; record the probe output in the ops runbook.
3. Cut one canary consumer (Atlantis `.terraformrc` → proxy origin) before any
   fleet move; keep the AWS mirror authoritative until the drift backstop is
   green for a week against Garage.

## Open Questions

1. **Degraded-mode posture when the backend lacks conditional writes and
   versioning?**
   - a. (Recommended) Fail closed by default; `apply` refuses degraded backends
     unless `--allow-unversioned-backend` is set, with the weakening logged to
     the audit trail. The dangerous failure shape (server 200s while ignoring
     preconditions) must never be the default.
   - b. Warn and proceed: print the warning but apply anyway, leaning entirely
     on the CI concurrency group.
   - c. Refuse S3-compatibles outright until Garage ships versioning and
     preconditions, revisiting then.
   - Other: \_\_\_
2. **Where should the endpoint configuration live?**
   - a. (Recommended) Optional `endpoint` / `path_style` on the `mirror` HCL
     block, overridable by `SLUICE_S3_ENDPOINT` / `SLUICE_S3_PATH_STYLE`. Keeps
     one manifest describing the whole mirror, reviewable in git.
   - b. A separate `backend` block decoupled from `mirror`, so one manifest can
     address several stores.
   - c. Environment only, no HCL surface — topology stays out of the manifest.
   - Other: \_\_\_
3. **How should Terraform/Atlantis read from the mirror?**
   - a. (Recommended) Reverse proxy in front of the website endpoint: path-style
     URLs for Terraform, plus TLS and IP allowlisting so the mirror is
     fleet-readable rather than world-readable.
   - b. Website endpoint directly (custom domain per bucket, `Host`-routed).
   - c. Authenticated S3 API reads with per-runner keys — no anonymous serving
     at all.
   - Other: \_\_\_
4. **How do CI runners authenticate to Garage for publish?**
   - a. (Recommended) Vault/env-injected static publisher key pair. No OIDC
     equivalent exists for Garage; key custody plus rotation is the control.
   - b. Short-lived keys minted via the Garage admin API at job start.
   - c. A small OIDC-bridging sidecar that trades GitHub OIDC for Garage keys.
   - Other: \_\_\_
5. **How broad should backend support be?**
   - a. (Recommended) Generic S3-compatible support behind the capability probe:
     full mode where honored, degraded mode where absent. One code path, new
     backends fall out of the checklist.
   - b. Garage-only hardcoded profile; generalize later if a second backend
     appears.
   - c. Explicit allowlist (Garage + MinIO), each with a pinned quirks profile.
   - Other: \_\_\_
6. **How do we preserve yank forensics without object versioning?**
   - a. (Recommended) Recycle-bin pattern: `apply` copies yanked artifacts to a
     `_retired/` prefix (retention-managed) before deleting. Cheap, keeps the
     forensic trail the audit log points at.
   - b. Accept permanent deletion; cosign signatures/attestations remain the
     tamper evidence.
   - c. Retire-by-tombstone: leave a signed `_retired/<key>.tombstone` marker
     instead of the bytes.
   - Other: \_\_\_

## References

- DESIGN-0001 sluice Provider Mirror CLI (the `Bucket` interface and `Cond`
  conditional-write contract); DESIGN-0003 Provider Mirror CI Pipelines
  (concurrency group, drift backstop, verifier); DESIGN-0002 Provider Mirror
  Bucket Terraform Module (the AWS posture this degrades from)
- RFC-0001 Terraform Provider Cache and Internal Provider Mirror
- Garage S3-compatibility reference (versioning stub, precondition gaps
  #1052/#1326, anonymous-access #263) and the website-endpoint cookbook
- `docs/sluice-spec.md` — HCL schema and exit-code contract
