# Provider Mirror Program Roadmap

Program-level view across the four design docs and the rollout. Detailed,
checkbox-tracked implementation lives in per-design IMPL docs **in the repo
where the work happens**, created when each workstream kicks off — this file
holds the sequencing, the cross-repo deliverables map, the rollout phase (which
belongs to no single design), and the program-level open questions.

History: this file began as IMPL-0001 "Provider Mirror Program" before the
tracking was split per-design (2026-08-01).

## Workstreams

| #   | Workstream            | Design      | Home repo             | Tracking                                                                                                                           | Gated on                 |
| --- | --------------------- | ----------- | --------------------- | ---------------------------------------------------------------------------------------------------------------------------------- | ------------------------ |
| 1   | sluice CLI            | DESIGN-0001 | sluice (this repo)    | [IMPL-0001](impl/0001-sluice-provider-mirror-cli.md)                                                                               | —                        |
| 2   | Mirror bucket module  | DESIGN-0002 | shared modules family | IMPL there at kickoff — reuse analysis in DESIGN-0002 (family covers baseline; OIDC role + deny statements + `mirror_url` are new) | —                        |
| 3   | Operational pipelines | DESIGN-0003 | sluice + governance   | split per repo at kickoff                                                                                                          | INV-0001 (verifier only) |
| 4   | Policy library        | DESIGN-0004 | governance repo       | IMPL there at kickoff                                                                                                              | —                        |
| 5   | Rollout and cutover   | program     | — (org-wide)          | this doc, below                                                                                                                    | 1–4                      |

## Status

- Workstream 1 is complete: IMPL-0001 is Completed and `sluice` v0.0.1 is
  released; DESIGN-0001 is Implemented.
- Workstreams 2–4 are not yet kicked off (no per-design IMPL docs yet);
  workstream 5 (rollout and cutover) remains gated on 1–4, so its tasks below
  stay unchecked.

## Sequencing

- Workstream 1 (CLI) is unblocked and self-contained; its phases are sequential
  internally.
- Workstream 2 (bucket module) can run fully in parallel with the CLI.
- Workstream 3 (pipelines) needs the CLI's publisher (`plan`/`apply`) and the
  bucket module's `publisher_role_arn`. The governance repo can be created at
  kickoff (topology resolved: single repo); only the verifier deliverable waits
  on [INV-0001](investigation/0001-verifier-runtime-shape-and-code-home.md).
- Workstream 4 (policy library) needs `sluice export` (CLI Phase 2); its home
  repo is the same governance repo.
- Workstream 5 is the cutover and comes last.

## Rollout and cutover

This is RFC territory (program Phases 2–4); the tasks here are the parts the
four designs own. Depends on all prior workstreams.

### Tasks

- [ ] `bootstrap` across live repos; review and merge the complete seeded
      manifest.
- [ ] Full apply; spot-check mirror `h1:` values against consumer lock files.
- [ ] Canary repo on exclusive `network_mirror` ahead of the fleet.
- [ ] Atlantis `.terraformrc` cutover to the mirror (feed `mirror_url` from the
      bucket module).
- [ ] Enable org-wide Renovate outflow.
- [ ] Yank drill: run the full runbook against a sacrificial version; measure
      CVE-to-org-block time.
- [ ] Flip DESIGN-0001..0004 to Implemented and the per-design IMPLs to
      Completed; `docz update`.

### Success Criteria

- 100% of Atlantis provider installs served from the mirror; no public registry
  egress from runners.
- Yank drill completes with a measured minutes-scale block time.
- Seven consecutive daily backstop runs with empty plans (steady-state
  determinism).

## Deliverables map

| Repo             | Artifact                                              | Workstream |
| ---------------- | ----------------------------------------------------- | ---------- |
| sluice           | `internal/{config,mirror,registry,hash,publish}`      | 1          |
| sluice           | `cmd/sluice` command wiring, exit codes               | 1          |
| sluice           | composite plan-comment action                         | 3          |
| per INV-0001     | mirror verifier (runtime shape + code home)           | 3          |
| shared modules   | mirror bucket module + libtftest suite                | 2          |
| governance repo  | manifest HCL, plan/apply/backstop workflows, Renovate | 3          |
| governance repo  | `policy/`, `defs/`, generator, bundle pipeline        | 4          |
| platform account | bucket root module, verifier deploy, Atlantis wiring  | 2, 3, 5    |

## Cross-cutting dependencies

- VPC self-hosted runners reaching the bucket's VPC endpoint (workstreams 3, 5).
- A sandbox AWS account for the bucket module's opt-in policy-evaluation tests
  (workstream 2).
- Atlantis deployment access for bundle pinning and `.terraformrc` cutover
  (workstreams 4, 5) — coordination point, not owned here.
- INV-0001 gates only the verifier deliverable (workstream 3); nothing else in
  workstreams 3–4 waits on it.

## Open Questions

None currently open. Resolved:

- **Per-design vs program tracking** (2026-08-01): per-design IMPL docs in each
  workstream's home repo, created at kickoff; this roadmap holds sequencing and
  the rollout phase. CLI-implementation questions (command framework, cosign
  integration, e2e oracle) moved to IMPL-0001.
- **Governance repo topology** (2026-08-01): one governance repo — manifest,
  policy library, and both pipelines together. The strongest reading of
  ADR-0001's "no second list": a yank is one diff that retracts from the mirror
  and cuts a bundle release, with no cross-repo sync automation to build or
  trust. DESIGN-0003's "approved-versions repository" and DESIGN-0004's
  "manifest in this same repo" now name the same repo.
- **Verifier runtime shape and code home** (2026-08-01): spun out to
  [INV-0001](investigation/0001-verifier-runtime-shape-and-code-home.md). The
  question widened past code home — a long-running sidecar consumer may fit the
  fleet better than the Lambda DESIGN-0003 assumed, possibly preferred. The INV
  decides the shape first; the code home falls out of it.

## References

- IMPL-0001 sluice Provider Mirror CLI (workstream 1 tracking)
- INV-0001 Verifier Runtime Shape and Code Home (workstream 3 gate)
- DESIGN-0001 sluice — Provider Mirror CLI
- DESIGN-0002 Provider Mirror Bucket Terraform Module
- DESIGN-0003 Provider Mirror CI Pipelines
- DESIGN-0004 Policy Library, Generator, and OCI Distribution
- RFC-0001 Terraform Provider Cache and Internal Provider Mirror
- ADR-0001 Policy Is Static, Data Is Exported, Input Is Never Generated;
  ADR-0002 Infrastructure Compliance via Policy-as-Code
- `docs/sluice-spec.md` — working spec (HCL schema, command surface, exit codes,
  yank runbook)
