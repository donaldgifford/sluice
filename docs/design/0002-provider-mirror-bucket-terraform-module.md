---
id: DESIGN-0002
title: "Provider Mirror Bucket Terraform Module"
status: Draft
author: Donald Gifford
created: 2026-07-27
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0002: Provider Mirror Bucket Terraform Module

<!--toc:start-->

- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Resources](#resources)
  - [Interface](#interface)
  - [Reuse of the shared S3 module family](#reuse-of-the-shared-s3-module-family)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

**Status:** Draft **Author:** Donald **Date:** 2026-07-26

## Overview

A Terraform module that provisions the S3 bucket serving the provider network
mirror, its integrity and access policies, and (optionally) the CI publisher IAM
role. The module encodes the security posture from the mirror RFC — VPC-only
anonymous reads, immutability, HTTPS-only — so the posture is reviewed once,
here, rather than re-derived per environment.

## Goals and Non-Goals

### Goals

- Single module producing a serving-ready mirror bucket with the full control
  set.
- Anonymous `s3:GetObject` restricted to declared VPC endpoints; unreachable
  otherwise.
- Deny-delete by default; optional Object Lock for stricter retention.
- Optional publisher role (GitHub OIDC) with write-only-no-delete permissions.
- Testable with libtftest (Terratest + LocalStack) and shipped in the shared
  modules family.

### Non-Goals

- Managing mirror _content_ (the mirror CLI's job).
- CloudFront or any off-VPC serving path.
- Cross-region replication — decided against for v1 (see Open Questions);
  re-`apply` from the manifest is the DR path.

## Background

The network mirror protocol requires only static HTTPS GETs, so the S3 REST
endpoint (`https://<bucket>.s3.<region>.amazonaws.com/`) is the serving layer.
Two AWS behaviors this design leans on: (1) a bucket policy conditioned on
`aws:sourceVpce` is evaluated as non-public, so all four Block Public Access
settings stay enabled; (2) anonymous reads cannot decrypt SSE-KMS objects, so
encryption is SSE-S3 by decision, not oversight.

## Detailed Design

### Resources

- `aws_s3_bucket` with `aws_s3_bucket_versioning` (enabled) and SSE-S3
  encryption configuration.
- `aws_s3_bucket_public_access_block` — all four settings `true`.
- Optional Object Lock (`enable_object_lock`, creation-time only — flagged
  prominently since it cannot be retrofitted).
- `aws_s3_bucket_policy` composed of four statements:
  1. **AllowMirrorReadFromVPCE** — `Principal: "*"`, `s3:GetObject`,
     `Condition: aws:sourceVpce ∈ var.vpc_endpoint_ids`.
  2. **DenyInsecureTransport** — deny all actions when
     `aws:SecureTransport = false`.
  3. **DenyObjectDeletion** — deny `s3:DeleteObject` and
     `s3:DeleteObjectVersion` to all principals, with an exception condition
     (`aws:PrincipalArn NotIn var.break_glass_principal_arns`, default empty →
     absolute deny; count-gated at render — unconditional when the list is
     empty, since an empty `NotIn` condition is invalid IAM).
  4. **DenyPolicyMutation** (optional, default on) — deny
     `s3:PutBucketPolicy`/`s3:DeleteBucketPolicy` outside
     `var.policy_admin_principal_arns`, so the deny-delete statement cannot be
     quietly removed.

- Optional lifecycle rule transitioning noncurrent object versions to IA after N
  days (cost control that preserves forensics; never expiration).
- Optional server access logging (`aws_s3_bucket_logging`) to
  `var.access_log_bucket` under `var.access_log_prefix` — the read-side
  forensics record. CloudTrail data events for writes are configured on the
  account trail, not managed here.
- Publisher IAM role (GitHub OIDC) assumed pre-existing and provisioned out of
  band — this module creates no IAM resources. Same-account: the role's own
  identity policy grants its writes (`s3:PutObject`, `s3:GetObject`,
  `s3:ListBucket`, `s3:GetBucketLocation`, no deletes) and the bucket policy
  needs no grant for it. Cross-account: the root module adds one injected allow
  for the role ARN. Either way the bucket-policy denies constrain it.

### Interface

Variables: `bucket_name`, `vpc_endpoint_ids` (list, required),
`enable_object_lock` (bool, false), `object_lock_retention_days`,
`break_glass_principal_arns` (list, default []), `policy_admin_principal_arns`,
`noncurrent_version_ia_days` (number, null disables), `access_log_bucket`
(string, null disables), `access_log_prefix`, `tags`.

Outputs: `bucket_id`, `bucket_arn`, `mirror_url` (REST endpoint **with trailing
slash**, ready to paste into `provider_installation`). No `publisher_role_arn`:
the publisher role is provisioned out of band and referenced directly by its
consumers.

### Reuse of the shared S3 module family

Reuse analysis (2026-09-14) against `modules/s3` (`bucket`, `evidence-bucket`,
`access-logs-bucket` over `internal/core`) and `modules/iam/role`: workstream 2
lands as a new purpose module (proposed `modules/s3/mirror-bucket`) wrapping
`internal/core` directly. `evidence-bucket` is not the template — it pins Object
Lock on with a mandatory retention duration and drags the Terragrunt
remote-state globals; the mirror needs lock optional (default off) and no
remote-state lookup on this path.

Composed verbatim from the family (no new primitives):

- Bucket, versioning (pinned on here), all-true public-access block, and
  ownership controls from the core baseline.
- SSE-S3 via `encryption = { mode = "s3" }` — anonymous reads cannot decrypt
  SSE-KMS objects, so the design's SSE-S3 decision stands.
- `DenyInsecureTransport` plus `DenyOldTls` — a superset of this design's
  HTTPS-only statement.
- VPCE restriction via `allowed_vpc_endpoint_ids` (`DenyOutsideVpce`); the
  `AllowMirrorReadFromVPCE` allow is carried by `additional_policy_statements`
  injection (the reserved-sid guard does not block it).
- Access logging via the core's explicit `logging = { target_bucket, prefix }`
  (the design's `access_log_bucket` / `access_log_prefix`, no fleet lookup); the
  sink itself is the existing `access-logs-bucket` module.
- Noncurrent-version IA transition (never expire) via `lifecycle_rules`.
- Test pattern throughout: libtftest plan suites asserting rendered policy JSON
  statement-by-statement, LocalStack apply, and tagged opt-in sandbox runs for
  policy-evaluation behavior.

New surface the mirror module owns (absent from the family):

- Publisher role placement — the GitHub OIDC publisher role is provisioned out
  of band (assumed pre-existing), so the module creates no IAM resources and
  exposes no role inputs or outputs. (For the record: `iam/role` trust is AWS
  principals only with no `Federated`/OIDC support, which is moot under this
  assumption.) Same-account needs no bucket-policy grant for the role;
  cross-account adds one injected allow at the root.
- `DenyObjectDeletion` with the break-glass exception — count-gated injection:
  an unconditional deny when `break_glass_principal_arns` is empty (a `NotIn []`
  condition is invalid IAM), a conditional `aws:PrincipalArn NotIn …` deny
  otherwise.
- `DenyPolicyMutation` guard (deny `PutBucketPolicy` / `DeleteBucketPolicy`
  outside `policy_admin_principal_arns`) — injected statement, new.
- `mirror_url` output — no family module outputs a serving URL today.
- Interface mapping: the design's plain `bucket_name` rides the family's
  `name_override` hatch; the six Terragrunt globals stay out.

## API / Interface Changes

New module in the shared modules repo; consumed initially by one root module in
the security platform account set, applied through Atlantis.

## Data Model

None beyond S3 bucket configuration. Object layout is owned by the mirror CLI.

## Testing Strategy

- libtftest harness: apply against LocalStack, assert versioning, encryption,
  and policy document content (decode the rendered policy JSON and assert
  statement-by-statement rather than string-matching).
- LocalStack does not faithfully enforce BPA/Object Lock semantics, so those
  paths get plan-time assertions (rendered values) plus a tagged, opt-in test
  against a real sandbox account for the policy-evaluation behavior (anonymous
  GET succeeds via VPCE, fails without; delete denied).
- Fixture added to the companion modules test repo; wire into the coverage
  reporting work in libtftest.

## Migration / Rollout Plan

1. Land module + tests; version and tag.
2. Root module in the platform account; Atlantis apply.
3. Feed `mirror_url` into the Atlantis `.terraformrc` mount and the out-of-band
   publisher role ARN into the approved-versions repo CI (see CI pipelines
   design).

## Open Questions

Open (2026-09-14, from the shared-family reuse analysis):

- **Wrapper shape**: thin wrapper over `internal/core` with this design's exact
  variable/output surface (recommended — see the reuse section) vs forking
  `evidence-bucket`. Decide at kickoff; either way the new statements and
  `mirror_url` are new code.

Resolved (2026-07-31):

- **Break-glass**: ship with `break_glass_principal_arns = []` — absolute deny.
  Content mistakes are fixed forward with new object versions; a true purge
  (malware takedown) is a reviewed PR that adds a principal, applies, deletes,
  and reverts. The escape hatch is the change process, not a standing role.
- **Read-path logging**: S3 server access logs to a logging bucket with
  lifecycle expiry; CloudTrail data events on writes only (write events are the
  ingest audit trail; per-event pricing on reads is the expensive part at
  provider-install volume). The trail's event selectors are account-level
  configuration outside this module. These write events do double duty: they are
  also the event source for the event-driven bucket verifier (DESIGN-0003).
- **Cross-region DR**: no replica in v1 — recovery is `sluice apply` from the
  manifest into the returned region or a new bucket. Accepted caveat: yanked
  zips retained for forensics exist only in this bucket and are not
  reconstructable from the manifest.
- **Publisher role ownership** (2026-09-14): the GitHub OIDC publisher role is
  assumed pre-existing out of band — the module creates no IAM resources and
  exposes no role inputs or outputs. Same-account roles need no bucket-policy
  grant; cross-account adds one injected allow at the root.

## References

- RFC: Terraform Provider Cache and Internal Provider Mirror
- DESIGN: Provider Mirror CLI; DESIGN: Provider Mirror CI Pipelines
- libtftest + companion modules repo
