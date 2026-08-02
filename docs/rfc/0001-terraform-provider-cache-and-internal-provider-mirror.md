---
id: RFC-0001
title: "Terraform Provider Cache and Internal Provider Mirror"
status: Draft
author: Donald Gifford
created: 2026-07-27
---

<!-- markdownlint-disable-file MD025 MD041 -->

# RFC 0001: Terraform Provider Cache and Internal Provider Mirror

**Status:** Draft **Author:** Donald Gifford **Date:** 2026-07-27

<!--toc:start-->
- [Summary](#summary)
- [Problem Statement](#problem-statement)
  - [Scale and reliability](#scale-and-reliability)
  - [Supply-chain exposure](#supply-chain-exposure)
- [Proposed Solution](#proposed-solution)
- [Design](#design)
  - [Cache server (Atlantis StatefulSet)](#cache-server-atlantis-statefulset)
  - [Mirror bucket and serving](#mirror-bucket-and-serving)
  - [Terraform CLI configuration](#terraform-cli-configuration)
  - [Mirror CLI](#mirror-cli)
  - [Enforcement and hygiene](#enforcement-and-hygiene)
- [Alternatives Considered](#alternatives-considered)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Cache server](#phase-1-cache-server)
  - [Phase 2: Mirror bootstrap](#phase-2-mirror-bootstrap)
  - [Phase 3: Cutover](#phase-3-cutover)
  - [Phase 4: Governance loop](#phase-4-governance-loop)
- [Risks and Mitigations](#risks-and-mitigations)
- [Success Criteria](#success-criteria)
- [References](#references)
<!--toc:end-->

## Summary

Terraform runs orchestrated by Atlantis pull providers directly from the public
HashiCorp registry on every `init`, which fails at our scale and leaves the
organization with no control over what provider code enters our environment.
This RFC proposes a two-layer fix: enable the Terragrunt provider cache server
on Atlantis to eliminate redundant downloads, and stand up an internal S3-backed
provider network mirror — managed declaratively by a small Go CLI — that becomes
the only permitted installation source. The mirror doubles as a supply-chain
control: an immutable, allowlisted, auditable inventory of every provider
version approved for use.

## Problem Statement

### Scale and reliability

Atlantis runs on Kubernetes with Terragrunt wrapping Terraform. Large `run-all`
operations span roughly 600 units, and each unit's `terraform init` downloads
its providers directly from `registry.terraform.io`. A single large run
therefore generates ~600 concurrent pulls of largely identical artifacts. The
observable symptoms:

- Registry rate limiting (HTTP 429) fails plans mid-run, forcing retries of
  already-expensive operations.
- Redundant downloads of multi-hundred-megabyte providers (the AWS provider
  alone is ~600 MB unpacked) inflate init time and network cost on every PR.
- Run reliability degrades in proportion to run size — exactly the runs where a
  retry hurts most.

Terraform's native answer, `TF_PLUGIN_CACHE_DIR`, is not viable here: the plugin
cache directory has no locking, and concurrent `init` processes racing on it
corrupt the cache. This is a known failure mode under Atlantis parallelism and
is why we do not use it today.

### Supply-chain exposure

Independent of scale, direct dependency on a public registry means:

- **No allowlist.** Any developer can introduce any provider from any namespace
  — including typosquatted or malicious packages — and nothing in the pipeline
  prevents it from being installed and executed with our cloud credentials.
- **No immutability.** Upstream can yank or force-remove versions at any time. A
  version we depend on can disappear, and a compromised release replaces a good
  one with no signal on our side.
- **No audit point.** There is no single place that records which provider code,
  at which version, entered the environment, when, and on whose approval.
- **Availability coupling.** Registry outages and rate-limit policy changes are
  our outages.

For a Security-owned platform serving ~1,500 developers across 200+ AWS
accounts, provider binaries are third-party code executed with privileged
credentials. They deserve the same ingestion controls we apply to any other
software dependency.

## Proposed Solution

Two layers with distinct jobs, plus a governance loop around them:

1. **Terragrunt provider cache server** (efficiency, ships immediately).
   Terragrunt's built-in cache server is a local process that downloads each
   provider version exactly once, with proper locking, and serves concurrent
   `init`s over the provider network mirror protocol on localhost. It was built
   specifically because the naive shared cache directory is unsafe under
   `run-all` concurrency. Enabled via environment on the Atlantis StatefulSet
   with its cache directory on the existing PVC, it collapses ~600 pulls per run
   into one fetch per provider version with no workflow changes.

2. **Internal S3-backed provider network mirror** (control). The network mirror
   protocol is static JSON and zip files over HTTPS — no server component
   required. We populate an S3 bucket with the protocol layout and configure
   Terraform (via a mounted CLI config) with an _exclusive_ `network_mirror`
   block. Under an exclusive mirror, the hostname in a provider source address
   is only an identifier, not an installation source: any provider or version
   not present in our bucket fails `init` by construction. The allowlist is
   structural, not policy-checked after the fact. The cache server honors this
   configuration, so the full chain becomes: units → cache server → S3 mirror →
   nothing external.

3. **Declarative mirror management** (governance). A small Go CLI (built on
   hclkit) reconciles desired state — HCL files declaring each provider and its
   approved versions — against actual state, which is the mirror's own protocol
   index files. `plan` shows versions to be added or removed; `apply` downloads
   from the origin registry, verifies the GPG signature over the SHA256SUMS and
   the artifact checksum, computes the `h1:` hash, and publishes to S3. The HCL
   lives in a Security-codeownered repo: a merged PR _is_ the approval record,
   CI applies on merge, and a scheduled plan detects drift. Renovate runs in two
   stages — proposing new upstream versions into the approved-versions repo, and
   fanning verified versions out to module repos via a custom datasource pointed
   at the mirror — so a version's presence in the bucket is the approval, and
   unverified versions never appear in a developer's PR.

## Design

### Cache server (Atlantis StatefulSet)

```yaml
env:
  - name: TG_PROVIDER_CACHE
    value: "1"
  - name: TG_PROVIDER_CACHE_DIR
    value: /atlantis/provider-cache
```

`TF_PLUGIN_CACHE_DIR` is never set. Pre-mirror (Phase 1), the cache directory
lives on the Atlantis PVC so cold starts don't hammer the public registry. Once
the exclusive mirror is live (Phase 3), the recommendation flips to `emptyDir`
with a `sizeLimit`: every provider rewarms from the in-VPC S3 mirror in seconds,
so persistence buys nothing — and cache invalidation becomes a deployment
restart. A yank merge then propagates to the cache via GitOps: Argo/Kargo
observes the policy-repo change and triggers a rollout restart of Atlantis,
wiping and rewarming the cache from a mirror that no longer serves the version.
No cache-management tooling exists or is needed.

### Mirror bucket and serving

- S3 bucket with versioning enabled and a deny-delete bucket policy (Object Lock
  if audit requirements harden further). Provider zips are never destroyed;
  removal from service is an index change, and CloudTrail is the audit log.
- Served directly from the HTTPS S3 REST endpoint. Bucket policy grants
  anonymous `s3:GetObject` conditioned on `aws:sourceVpce`, keeping the bucket
  unreachable from outside the VPC with no CloudFront, certificates, or running
  service to operate.
- Protocol layout per provider: `<hostname>/<namespace>/<type>/index.json`
  (versions), `<version>.json` (platforms, archive paths, `h1:` hashes), and the
  zips.

### Terraform CLI configuration

Mounted on Atlantis pods (and available to developer environments later):

```hcl
provider_installation {
  network_mirror {
    url = "https://<bucket>.s3.<region>.amazonaws.com/"
  }
}
```

No `direct` block: installation outside the mirror is impossible rather than
discouraged.

### Mirror CLI

- **Desired state:** HCL, one block per provider keyed by full source address,
  merged across a directory or read from a single file; a `mirror {}` block
  carries bucket, region, and default platform matrix.
- **Actual state:** the bucket's `index.json` files. No state file and no
  parallel HCL copy of the index — the protocol artifacts terraform reads are
  the record of what is deployed.
- **Verification at ingest:** origin registry download, GPG signature
  verification of `SHA256SUMS`, artifact checksum verification, `h1:`
  computation via `dirhash.HashZip` (the same function Terraform uses).
- **Publish ordering:** zips, then `<version>.json`, then `index.json` last,
  with a conditional write (ETag precondition) on the index. A crashed apply
  never publishes a version that cannot be fetched; concurrent CI runs cannot
  clobber each other.
- **Ergonomics:** `-detailed-exitcode` for CI gating and `-json` plan output for
  PR comments; humans write HCL, machines exchange protocol JSON, and the JSON
  never appears in a pull request.

### Enforcement and hygiene

- Atlantis server-side repo config sets `allowed_overrides: []`, closing the
  bypass surface of repo-level workflows and injected environment
  (`TF_CLI_CONFIG_FILE`, `TF_PLUGIN_CACHE_DIR`, generated CLI configs).
- The Atlantis Conftest policy stage audits plan JSON for allowlisted provider
  hostnames and checks `.terraform.lock.hcl` presence with `h1:` hashes.
- repo-guardian's PR gate enforces the same lock-file and Terragrunt hygiene
  org-wide, covering execution paths outside Atlantis.
- Lock files must carry `h1:` hashes for all execution platforms
  (`terraform providers lock -platform=linux_amd64 ...`), since mirrors verify
  against `h1:` only.

## Alternatives Considered

- **`TF_PLUGIN_CACHE_DIR` on the Atlantis PVC.** Rejected: no locking;
  corruption under parallel init is a known failure mode and the original
  motivation for the Terragrunt cache server.
- **Registry proxy / mirror services** (Artifactory Terraform repos,
  boring-registry, hermitcrab, terraform-registry-mirror). These add a running
  service, its availability, patching, and authn surface to solve a problem
  static files solve outright. The static S3 mirror is IAM-native, has no
  runtime to operate, and its "API" is `GetObject`. Rejected for operational
  surface; revisit only if we need dynamic behavior (e.g., per-team visibility).
- **CloudFront in front of the bucket.** Unnecessary while all consumers are
  inside the VPC; the REST endpoint plus a VPC-endpoint-conditioned policy is
  simpler and keeps traffic private. Revisit if the mirror must serve laptops
  off-VPN.
- **Cache server only, no mirror.** Solves the 429s but none of the supply-chain
  problems: no allowlist, no immutability, no audit point, and cold starts still
  depend on the public registry.
- **Do nothing.** Large-run reliability continues to degrade and the
  supply-chain exposure stands.

## Implementation Phases

### Phase 1: Cache server

Enable `TG_PROVIDER_CACHE` on Atlantis with a PVC-backed cache directory.
Independent of all other phases; eliminates most 429s immediately.

### Phase 2: Mirror bootstrap

Provision the bucket, policies, and VPC endpoint condition. Inventory providers
and versions actually in use from lock files across repos to seed the initial
approved-versions HCL. Populate the mirror and validate serving with a canary
repo before any enforcement.

### Phase 3: Cutover

Mount the exclusive `network_mirror` CLI config on Atlantis. Anything failing
init at this point is an unapproved provider surfaced by construction; triage
additions through the approved-versions repo. Cutover also flips the cache
volume from PVC to `emptyDir` and wires the Argo/Kargo rollout-restart trigger
for yank propagation.

### Phase 4: Governance loop

Mirror CLI in CI (plan on PR, apply on merge, scheduled drift plan), CODEOWNERS
to Security, two-stage Renovate, Conftest policies, and repo-guardian checks.

## Risks and Mitigations

| Risk                                                         | Impact                   | Likelihood | Mitigation                                                                                        |
| ------------------------------------------------------------ | ------------------------ | ---------- | ------------------------------------------------------------------------------------------------- |
| Cutover breaks repos using unmirrored providers              | Failed plans             | Medium     | Phase 2 lock-file inventory seeds the manifest; canary repo before org-wide cutover               |
| Lock files missing `h1:` hashes fail init against the mirror | Failed plans             | Medium     | Pre-cutover sweep running `terraform providers lock`; repo-guardian check prevents regression     |
| Approval repo becomes a bottleneck for new versions          | Developer friction       | Medium     | Renovate automates the proposal PR; approval is a review + merge, not a ticket                    |
| Cache volume fills                                           | Failed inits on Atlantis | Low        | `emptyDir` sizeLimit sized for the version matrix; restart rewarms from the mirror; disk alerting |
| Mirror publish race or partial publish                       | Unfetchable version      | Low        | Index-last publish ordering; conditional writes; single-writer CI                                 |
| Out-of-band bucket modification                              | Integrity drift          | Low        | Deny-delete policy, versioning, scheduled `plan` drift detection, CloudTrail                      |

## Success Criteria

- Zero registry 429s on large Atlantis runs; measurable reduction in p95 `init`
  time for `run-all` operations.
- 100% of provider installations resolve from the mirror; a provider absent from
  the mirror cannot be installed anywhere in the pipeline.
- Every provider version in production maps to a reviewed, merged approval PR;
  drift plans run clean.
- A compromised or yanked upstream version can be removed from service org-wide
  with a one-line HCL change and a reviewable plan.

## References

- Terraform provider network mirror protocol —
  developer.hashicorp.com/terraform/internals/provider-network-mirror-protocol
- `terraform providers mirror` command —
  developer.hashicorp.com/terraform/cli/commands/providers/mirror
- Terragrunt Provider Cache Server — terragrunt.gruntwork.io
  (features/provider-cache-server)
- Prior art: straubt1/terraform-network-mirror,
  plus3it/tardigrade-terraform-network-mirror, seal-io/hermitcrab
- Related internal work: Burrito evaluation (Atlantis replacement),
  repo-guardian PR-gate enforcement plane
