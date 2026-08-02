# CLAUDE.md

Per-repo orientation for `donaldgifford/sluice`. This file is a Go-shaped
overlay on top of the universal homelab `CLAUDE.md` (see
[homelab/docs](https://github.com/donaldgifford/docs)); the universals apply
here too — only guidance specific to this repo is captured below.

## What this is

`sluice` is a Go CLI that manages an S3-backed Terraform provider network mirror
from a declarative HCL manifest — plan/apply semantics, cryptographic
verification at ingest. The full picture lives in `docs/` (see below); start
with `docs/sluice-spec.md` and `docs/roadmap.md`.

- Single binary under `cmd/sluice/`; library code under `internal/` (private to
  the module).
- GitHub-hosted (`github.com/donaldgifford/sluice`); released as multi-arch
  (linux+darwin × amd64+arm64) archives via `goreleaser`. No container image —
  the CLI ships as archives only.

## Layout

```text
cmd/sluice/          # main package — keep thin; wire commands, call internal/
internal/            # library code; not importable outside this module
docs/                # docz-managed: rfc/ adr/ design/ impl/ investigation/
docs/sluice-spec.md  # working spec: HCL schema, commands, exit codes
docs/roadmap.md      # program roadmap across the mirror workstreams
.goreleaser.yml      # release config (multi-arch archives + checksums + SBOM)
mise.toml            # pinned go + golangci-lint + goreleaser + universal tools
justfile             # `just` task runner — `just` for the menu
.github/workflows/   # CI (GitHub Actions)
```

## Workflows

**`just`, not `make` — and just targets before ad-hoc shell.** Commonly rerun
commands and tools get a just target so human and Claude run things identically;
if you find yourself shelling out the same command twice, add a target. `just`
prints the menu.

The load-bearing targets:

- `just build` / `just run -- <args>` — build to `build/bin/sluice` / run.
- `just test` — race detector; `just test-pkg ./internal/foo` for one package;
  `just test-integration` for `//go:build integration` suites.
- `just test-coverage` + `just coverage-gate` — coverage profile, then the
  per-package `internal/` floor check CI enforces.
- `just lint` / `just fmt` — every linter / formatter (Go, YAML, Markdown,
  Actions).
- `just changelog` — regenerate `CHANGELOG.md`; `just changelog-check` mirrors
  CI's drift check.
- `just check` — pre-commit gate (lint + test).
- `just ci` — everything CI runs, locally. Green here means green in CI.

### Release

- Merging to `main` auto-releases: `pr-semver-bump` reads the PR's semver label
  (`major`/`minor`/`patch`; `dont-release` skips), tags, and goreleaser
  publishes archives + checksums + syft SBOMs, GPG-signed. Every PR needs
  exactly one of those labels — `pr-labels.yml` enforces it.
- `just release v0.1.0` exists for manual tag + push; `just release-local`
  builds a snapshot without publishing.
- Version metadata is injected via `-ldflags`: `main.version`, `main.commit`,
  `main.date`. `--version`-style output should print these.

## Go-specific conventions

- **`go.mod` go directive matches `mise.toml`** (currently `go 1.26.5`). Bump
  both together — Renovate handles both, but keep them in one commit.
- **No `vendor/`**. Modules are resolved at build time.
- **`internal/` is a hard wall** — packages there can't be imported by other
  modules. Use it liberally; promote to a separate module only when something
  outside this repo actually needs it.
- **SPDX headers + `doc.go`**: every `.go` file starts with the goheader
  template (SPDX line + copyright), then a **blank line**, then the package
  clause — the blank line keeps the header out of godoc. Package documentation
  lives in a per-package `doc.go`, which is also where the package comment
  satisfies revive without fighting goheader.
- **Coverage floor**: `internal/` packages hold ≥60% coverage
  (`just coverage-gate`); CI fails under it.
- **`slog` for structured logs**, not `log` or third-party loggers. Set the
  default handler in `main()` so library code doesn't have to thread loggers.
- **No `init()` for behavior**. `init()` runs at import time — it breaks test
  isolation and surprises future-you. Wire dependencies in `main()`.
- **Tests live next to the code** (`foo_test.go` alongside `foo.go`).
  Integration tests that need external services go under
  `//go:build integration` and run via `just test-integration`.
- **Errors wrap with `%w`**: `fmt.Errorf("loading config: %w", err)`. Top of the
  call stack handles via `errors.Is` / `errors.As`.

## CI matrix

All GitHub Actions, on push/PR to `main` unless noted:

- `ci.yml` — lint, race tests, coverage gate, build (the `just ci` set).
- `changelog.yml` — byte-for-byte drift check that `CHANGELOG.md` matches
  git-cliff output; `changelog-regen.yml` regenerates it post-merge.
- `release.yml` — the label-driven auto-release above (push to `main`).
- `pr-labels.yml` — requires exactly one semver label per PR.
- `codeql.yml`, `security.yml`, `trufflehog.yml`, `license-check.yml` — static
  analysis, vuln scanning, secret scanning, license allow-list.
- `dependabot-severity-label.yml` — adds severity labels to Dependabot PRs.

## Dependencies: Renovate vs Dependabot

- **Renovate owns routine version bumps** — `go.mod` via the Go module manager,
  `mise.toml` via a custom regex manager configured upstream in
  `donaldgifford/renovate-config` (org-level config; no renovate file in this
  repo).
- **Dependabot is security-only** — `open-pull-requests-limit: 0` in
  `.github/dependabot.yml` disables version updates; its only job is opening PRs
  when the GitHub Advisory feed flags a CVE in the dep tree.

## Gotchas

- **`CHANGELOG.md` is generated** by git-cliff and verified byte-for-byte in CI
  — never hand-edit it; run `just changelog`. Both prettier (`.prettierignore`)
  and markdownlint (`.markdownlint-cli2.yaml` `ignores`) are configured to leave
  it alone.
- **docz owns `docs/` indexes and ToCs** — the README tables and
  `<!--toc:start-->` blocks are regenerated by `docz update`; don't hand-edit
  them. `docs/.markdownlint-cli2.yaml` relaxes MD051/MD024 for docz output only
  — the root config still enforces them everywhere else.
- **goreleaser v2 config**: v1 → v2 moved `archives[].format` to
  `archives[].formats` (slice). If you copy a pre-v2 `.goreleaser.yml` from
  elsewhere, validate with `just release-check`.
