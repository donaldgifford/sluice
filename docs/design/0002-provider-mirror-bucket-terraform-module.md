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
     absolute deny).
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
- Optional `aws_iam_role` publisher: GitHub OIDC trust locked to
  `var.publisher_repo_subjects` (e.g.
  `repo:org/approved-providers:ref:refs/heads/main`); permissions
  `s3:PutObject`, `s3:GetObject`, `s3:ListBucket`, `s3:GetBucketLocation` on
  this bucket only. No delete actions — defense in depth atop the bucket deny.

### Interface

Variables: `bucket_name`, `vpc_endpoint_ids` (list, required),
`enable_object_lock` (bool, false), `object_lock_retention_days`,
`create_publisher_role` (bool), `publisher_repo_subjects` (list),
`break_glass_principal_arns` (list, default []), `policy_admin_principal_arns`,
`noncurrent_version_ia_days` (number, null disables), `access_log_bucket`
(string, null disables), `access_log_prefix`, `tags`.

Outputs: `bucket_id`, `bucket_arn`, `mirror_url` (REST endpoint **with trailing
slash**, ready to paste into `provider_installation`), `publisher_role_arn`.

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
3. Feed `mirror_url` into the Atlantis `.terraformrc` mount and
   `publisher_role_arn` into the approved-versions repo CI (see CI pipelines
   design).

## Open Questions

None currently open. Resolved (2026-07-31):

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

## References

- RFC: Terraform Provider Cache and Internal Provider Mirror
- DESIGN: Provider Mirror CLI; DESIGN: Provider Mirror CI Pipelines
- libtftest + companion modules repo
