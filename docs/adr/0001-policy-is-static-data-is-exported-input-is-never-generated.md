---
id: ADR-0001
title: "Policy Is Static, Data Is Exported, Input Is Never Generated"
status: Proposed
author: Donald Gifford
created: 2026-07-27
---

<!-- markdownlint-disable-file MD025 MD041 -->

# 0001. Policy Is Static, Data Is Exported, Input Is Never Generated

<!--toc:start-->

- [Status](#status)
- [Context](#context)
- [Decision](#decision)
- [Consequences](#consequences)
  - [Positive](#positive)
  - [Negative](#negative)
  - [Neutral](#neutral)
- [Alternatives Considered](#alternatives-considered)
- [References](#references)
<!--toc:end-->

## Status

Proposed

## Context

The policy repo holds both the Rego policy library and the sluice manifest (the
approved-providers HCL). Because sluice speaks HCL and Conftest can _parse_ HCL,
it is tempting to conclude that "the conftest file" should be generated from the
manifest — but that conflates the three distinct pieces of every Conftest
evaluation:

- **Policy** — the Rego rules. The law.
- **Data** — reference material the rules consult through `data.*` (the provider
  allowlist). The approved list the law cites.
- **Input** — the artifact on trial at a given enforcement point: plan JSON in
  Atlantis, a `.terraform.lock.hcl`, the `*.tf` files in a module repo. The
  defendant.

One invocation shows all three:

```sh
conftest test .terraform.lock.hcl \
  --policy policy/provider/ \
  --data data/providers.json
```

Inside the Rego, `input.provider` is the parsed lock file and `data.providers`
is the allowlist. Conftest parses inputs itself at check time; Rego data
documents are JSON.

Without a recorded decision, each of these three is a candidate for code
generation, and generating the wrong one either duplicates the manifest parser
(drift risk) or produces Rego codegen nobody needs.

## Decision

1. **Policy is static.** The Rego rules are hand-written once, tested, and
   reviewed like any code. Rules are never generated from HCL or anything else:
   Rego's design already separates logic from data, so a new approved version
   never requires a rule change.
2. **Data is exported, not authored.** `data/providers.json` is the output of
   `sluice export` — the canonical JSON projection of the manifest
   (`{"providers": {"<addr>": ["<version>", ...]}}`). It is committed in this
   repo and verified by a `--check` diff in CI. No other tool parses the
   manifest; the generator's remaining data documents (module origins,
   exceptions export) come from their own systems of record.
3. **Input is never generated.** Whatever artifact an enforcement point already
   has is the input; Conftest parses it at check time.
4. **No HCL is ever generated.** HCL is exclusively what humans write (the
   manifest). The two machine formats are both projections of it:

```text
manifest.hcl ──sluice apply──▶  S3 mirror protocol JSON   (what CAN be installed)
manifest.hcl ──sluice export──▶ data/providers.json       (what's ALLOWED in a run)
```

Same parser, same source of truth, two projections. `export` is to the policy
layer exactly what `apply` is to the mirror.

## Consequences

### Positive

- Mirror content and policy data cannot disagree — one parser produces both, and
  a yank PR shows the manifest edit and the `data/providers.json` diff side by
  side; one merge rebuilds mirror and bundle.
- Rule changes and allowlist changes have naturally different review weight:
  logic diffs are rare and scrutinized, data diffs ride manifest merges.
- No second implementation of the manifest schema exists anywhere to drift.

### Negative

- The `sluice export` output schema becomes a public contract: field names, key
  ordering, and determinism are frozen, and changes to it are breaking changes
  for the policy bundle build.
- The policy repo takes a build-time dependency on the sluice binary (pinned
  version) for the `--check` verification.

### Neutral

- Committed generated data means `data/providers.json` appears in diffs; that is
  the point, but reviewers must know never to hand-edit it.

## Alternatives Considered

- **Generate the Rego from the manifest.** Rejected: codegen and review noise
  with zero enforcement gain; data-driven rules already exist in Rego's
  evaluation model.
- **Policy generator re-parses the manifest HCL.** Rejected: a second parser for
  sluice's schema is a drift machine; the schema belongs to sluice.
- **Build data at bundle time only (not committed).** Rejected: loses the
  atomic-review property — the yank PR would no longer show the allowlist change
  alongside the manifest change.

## References

- RFC: Infrastructure Compliance via Policy-as-Code
- DESIGN: Policy Library, Generator, and OCI Distribution (worked example
  section)
- DESIGN: sluice — Provider Mirror CLI (`export` command)
- sluice spec (`export` section)
