---
id: INV-0001
title: "Verifier Runtime Shape and Code Home"
status: Open
author: Donald Gifford
created: 2026-08-02
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV 0001: Verifier Runtime Shape and Code Home

**Status:** Open **Author:** Donald Gifford **Date:** 2026-08-02

<!--toc:start-->

- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Findings](#findings)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

What runtime shape should the mirror integrity verifier take — the
EventBridge-triggered Lambda as written in DESIGN-0003, or a long-running
sidecar service consuming the same events via a queue — and, downstream of that
choice, where does its code live?

The behavior is fixed either way (CloudTrail write data events → EventBridge →
verify the writing principal and the artifact's cosign signature, with a grace
window for sig-after-zip ordering, and the daily scheduled plan as the
level-triggered backstop). Only the compute shape and repo home are open.

## Hypothesis

A sidecar (long-running service on existing homelab infrastructure, fed by
EventBridge → SQS) is operationally preferable here: it reuses the deploy,
observability, and testing patterns the fleet already has, and a plain binary
consuming a queue is easier to run locally and in CI than a packaged Lambda. The
classic Lambda advantages (no host to keep alive, scale-to-zero) matter less in
an environment that already runs long-lived services next to Atlantis.

Expected code-home outcome: whichever shape wins, the verifier is a second small
binary target in the sluice repo — both shapes want to reuse `internal/mirror`
protocol types and the cosign verification path rather than reimplement them,
and goreleaser here already builds multiple targets. The shape decision picks
the packaging (Lambda bundle vs container image), not the repo.

## Context

Roadmap Open Question 2 asked only where the verifier's code lives; answering it
surfaced the prior question of what the verifier _is_. DESIGN-0003 assumed
Lambda without weighing a long-running consumer, and a sidecar may be the better
fit — possibly preferred — given existing infrastructure. Workstream 3's
verifier deliverable is gated on this investigation; the rest of workstream 3 is
not.

**Triggered by:** DESIGN-0003 (integrity verification), `docs/roadmap.md` Open
Question 2 (resolved 2026-08-01 by spinning out this INV)

## Approach

1. Sketch both event paths end to end: EventBridge → Lambda (retry + DLQ
   semantics) vs EventBridge → SQS → sidecar poller (visibility timeout,
   retention, DLQ). Confirm CloudTrail data-event delivery works identically
   into both targets.
2. Enumerate the downtime story for each: what happens to events while the
   verifier is broken or being deployed, how long delivery survives (Lambda
   async retry window vs SQS retention), and how each gap is closed by the daily
   backstop plan.
3. Compare IAM and network blast radius: the Lambda's execution role vs the
   sidecar's pod identity reaching the bucket's VPC endpoint from the cluster.
4. Compare build/test/deploy friction in this fleet specifically: Go Lambda
   packaging + LocalStack testing vs a container image on the existing cluster
   patterns (the honest weight of "another deployment model" vs "another
   always-on service").
5. Estimate steady-state and burst cost for both (publish events are rare; both
   should be near-free — verify that intuition).
6. Decide shape, then confirm the code home falls out as hypothesized (sluice
   second binary) or document why not.

## Findings

_To be filled in during the investigation._

## Conclusion

**Answer:** _Pending._

## Recommendation

_Pending._ On conclusion: record the decision in `docs/roadmap.md` (Resolved),
update DESIGN-0003's integrity-verification bullet if the shape changes from
Lambda, and unblock workstream 3's verifier deliverable (IMPL in its home repo
at kickoff).

## References

- DESIGN-0003 Provider Mirror CI Pipelines — integrity verification and drift
  backstop
- `docs/roadmap.md` — workstream 3, Resolved: verifier spun out to this INV
- DESIGN-0002 Provider Mirror Bucket Terraform Module — CloudTrail write data
  events decision (the event source)
- IMPL-0001 sluice Provider Mirror CLI — `internal/` packages both shapes would
  reuse
