---
id: ADR-0002
title: "Infrastructure Compliance via Policy-as-Code"
status: Proposed
author: Donald Gifford
created: 2026-07-27
---

<!-- markdownlint-disable-file MD025 MD041 -->

# RFC 0002: Infrastructure Compliance via Policy-as-Code

<!--toc:start-->

- [Summary](#summary)
- [Problem Statement](#problem-statement)
- [Proposed Solution](#proposed-solution)
- [Design](#design)
- [Alternatives Considered](#alternatives-considered)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Library and pipeline](#phase-1-library-and-pipeline)
  - [Phase 2: Atlantis integration](#phase-2-atlantis-integration)
  - [Phase 3: repo-guardian and CI](#phase-3-repo-guardian-and-ci)
  - [Phase 4: Module source governance](#phase-4-module-source-governance)
  - [Phase 5: Exceptions integration](#phase-5-exceptions-integration)
- [Risks and Mitigations](#risks-and-mitigations)
- [Success Criteria](#success-criteria)
- [References](#references)
<!--toc:end-->

**Status:** Draft **Author:** Donald **Date:** 2026-07-26

## Summary

Infrastructure compliance today is enforced piecemeal: repo-guardian gates
repository hygiene, Atlantis is about to gain Conftest checks for
provider-mirror enforcement, and everything else is review culture. This RFC
proposes a unified policy-as-code program: a single Security-owned Rego policy
library, tested and versioned like software, published as signed OCI artifacts
to the internal registry, and consumed at every enforcement point — Atlantis,
repo-guardian, reusable CI workflows, and developer machines — with exceptions
routed through the shared exceptions/approvals service as recorded, expiring
statements of record.

## Problem Statement

- **Checks are point solutions.** The provider-mirror rollout introduces
  Conftest policies in Atlantis; repo-guardian enforces its own checks; nothing
  shares logic, fixtures, or a release process. The next control means another
  bespoke implementation.
- **No distribution or versioning story.** Policies copied into repos or baked
  into images drift silently. There is no way to say "the org runs policy bundle
  v1.4.2" — which is exactly the statement an auditor or incident review needs.
- **No exception mechanism.** A blocking check with no waiver path gets disabled
  under delivery pressure; ad-hoc waivers with no expiry become permanent holes.
  Both outcomes are worse than the control working as designed.
- **Module sources are ungoverned.** Terraform module calls can reference any
  git ref or URL on the internet — the same supply-chain exposure the provider
  mirror closes, one layer up. There is currently no requirement that modules
  come from a registry or approved origin.
- **Data-driven checks have no source of truth.** Allowlists (provider
  hostnames, approved versions, module origins) exist implicitly in several
  places; policies need to consume them from the systems that own them, not
  restate them.

## Proposed Solution

1. **One policy library.** A dedicated repo of Rego policies organized by domain
   — provider governance, module source governance, resource baselines
   (encryption, tagging, public exposure), Terragrunt/workflow hygiene — with
   unit tests, coverage, and per-policy documentation. Security owns the repo;
   domain teams contribute via CODEOWNERS.
2. **Signed, versioned OCI distribution.** Conftest natively pushes and pulls
   policy bundles as OCI artifacts. Bundles are released with semver tags to the
   internal OCI registry, signed with cosign, and every consumer pins a version
   and verifies the signature. Renovate bumps consumer pins like any other
   dependency, so "what policy ran where, when" is answerable from git.
3. **Consistent enforcement points, one policy source.** Atlantis's Conftest
   policy stage pulls the pinned bundle; repo-guardian's PR gate evaluates the
   same bundle against repo content; reusable CI workflows offer pre-merge
   evaluation; developers can run the identical bundle locally. One
   implementation of each rule, four places it runs.
4. **Data from systems of record.** Generated data documents feed the policies:
   the approved-providers HCL manifest generates the provider allowlist, the
   module registry generates the module-origin allowlist, and the exceptions
   service exports active waivers. Policies stay logic; data stays owned.
5. **Exceptions as first-class records.** A failing check links to the
   exceptions/approvals service; an approved exception (scoped, justified,
   expiring) lands in the exported waiver data and the specific finding passes
   with an annotation. Enforcement pressure stays on, and every hole is
   documented, owned, and temporary.
6. **Module source governance.** Policies assert every module call resolves from
   approved origins — the internal module registry or tagged releases in
   approved orgs — and reject arbitrary git/HTTPS sources. This extends the
   mirror's "presence is approval" model to modules and creates the forcing
   function for publishing modules to a registry.

## Design

At RFC altitude only; the policy repo layout, generator tooling, and OCI
mechanics are specified in the companion DESIGN doc.

- **Evaluation inputs:** Terraform plan JSON (Atlantis), raw HCL and repo
  metadata (repo-guardian, pre-merge), Kubernetes manifests as a later domain.
- **Severity model:** `deny` (blocking) and `warn` (advisory); every new policy
  ships as `warn` for a defined bake period before promotion to `deny`.
- **Bundle contract:** a bundle is policies + generated data + metadata
  (version, provenance, signature). Consumers never assemble policies from
  source at run time.
- **Metrics:** evaluation results emitted centrally — violation rates by policy
  and team, exception counts and ages, bundle version adoption. Compliance
  posture becomes a dashboard, not an assertion.

## Alternatives Considered

- **OPA bundle server / styra-style control plane.** A running service with
  availability, auth, and upgrade burden; OCI artifacts give versioned, signed,
  cached distribution using registry infrastructure that already exists.
- **HashiCorp Sentinel.** Tied to TFC/TFE, which we do not run; Rego runs
  everywhere we enforce.
- **Per-repo vendored policies.** Guaranteed drift; version skew becomes
  unanswerable.
- **CI-only enforcement without Atlantis/repo-guardian integration.** Bypassable
  by anyone who can edit workflows; enforcement must live at the choke points
  users cannot modify.
- **Status quo.** Each new control is another bespoke implementation, and the
  module supply-chain gap stays open.

## Implementation Phases

### Phase 1: Library and pipeline

Policy repo with the initial provider-governance and lock-file policies (lifted
from the mirror rollout), test/coverage CI, OCI publish with signing.

### Phase 2: Atlantis integration

Policy stage pulls the pinned, signature-verified bundle; mirror-enforcement
policies switch from repo-local files to the bundle.

### Phase 3: repo-guardian and CI

repo-guardian evaluates the bundle in its PR gate; reusable workflow published
for pre-merge use; local developer tooling documented.

### Phase 4: Module source governance

Module-origin policy ships as `warn` with an adoption dashboard; registry
publication path for module authors lands; promotion to `deny` per
module-consuming repo cohort.

### Phase 5: Exceptions integration

Waiver export from the exceptions/approvals service wired into bundle data;
annotated-pass behavior in enforcement points.

## Risks and Mitigations

| Risk                                                   | Impact                                  | Likelihood | Mitigation                                                                                      |
| ------------------------------------------------------ | --------------------------------------- | ---------- | ----------------------------------------------------------------------------------------------- |
| Policy false positives block delivery                  | Developer friction, pressure to disable | Medium     | Warn-first bake period; exception path from day one; violation dashboards before promotion      |
| Bundle pin lag (consumers on old policy)               | Stale enforcement                       | Medium     | Renovate bumps pins; adoption metric with SLO                                                   |
| Registry unavailability blocks Atlantis policy stage   | Plans blocked                           | Low        | Pull-through cache on runners; fail-open vs fail-closed decision recorded per enforcement point |
| Module registry requirement stalls on migration effort | Phase 4 slips                           | Medium     | Warn-mode telemetry sizes the migration before any deadline; publish tooling shipped first      |
| Exception volume overwhelms review                     | Rubber-stamping                         | Medium     | Expiry by default, aging reports, approval routing by domain                                    |

## Success Criteria

- Every enforcement point evaluates the same signed bundle version, and that
  version is answerable from git for any point in time.
- The mirror-enforcement policies run from the bundle in Atlantis and
  repo-guardian with zero policy logic duplicated.
- 100% of module calls in managed repos resolve from approved origins, or carry
  a live exception.
- Every active waiver has an owner, justification, and expiry in the exceptions
  service; zero out-of-band policy disables.

## References

- RFC: Terraform Provider Cache and Internal Provider Mirror
- DESIGN: Policy Library, Generator, and OCI Distribution (companion)
- Exceptions and approvals service design; repo-guardian
- Conftest OCI push/pull — conftest.dev; Open Policy Agent — openpolicyagent.org
