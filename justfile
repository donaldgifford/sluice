# sluice — task runner
#
# Project automation via just. Run `just` with no arguments for the menu.

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

project_name      := "sluice"
project_owner     := "donaldgifford"
go_package        := "github.com/" + project_owner + "/" + project_name
build_dir         := "build"
bin_dir           := build_dir + "/bin"
coverage_out      := "coverage.out"
coverage_floor    := "60"
allowed_licenses  := "Apache-2.0,MIT,BSD-2-Clause,BSD-3-Clause,ISC,MPL-2.0"

# Version info derived from git; falls back to dev when not in a repo or tag-less.
commit_hash := `git rev-parse --short HEAD 2>/dev/null || echo unknown`
version     := `git describe --tags --always --dirty 2>/dev/null || echo dev`
build_date  := `date -u +%Y-%m-%dT%H:%M:%SZ`

# Default: list recipes
_default:
    @just --list --unsorted

# ─── Build ──────────────────────────────────────────────────────────

# Build the CLI binary into build/bin/sluice
[group('build')]
build:
    @mkdir -p {{ bin_dir }}
    @go build -ldflags "-X main.version={{ version }} -X main.commit={{ commit_hash }} -X main.date={{ build_date }}" \
        -o {{ bin_dir }}/{{ project_name }} ./cmd/{{ project_name }}
    @echo "✓ Built {{ bin_dir }}/{{ project_name }} ({{ version }})"

# Remove build artifacts, release output, and the coverage profile
[group('build')]
clean:
    @rm -rf {{ build_dir }}/ dist/
    @rm -f {{ coverage_out }}
    @find . -name "*.test" -delete
    @echo "✓ Cleaned build artifacts"

# ─── Run ────────────────────────────────────────────────────────────

# Build then run the CLI: just run plan -config-dir ./approved-providers
[group('run')]
run *ARGS: build
    @{{ bin_dir }}/{{ project_name }} {{ ARGS }}

# ─── Test ───────────────────────────────────────────────────────────

# Run all tests with the race detector
[group('test')]
test:
    @go test -race ./...

# Run tests for a single package: just test-pkg ./internal/mirror
[group('test')]
test-pkg pkg:
    @go test -v -race {{ pkg }}

# Run integration tests (//go:build integration), which need external services
[group('test')]
test-integration:
    @go test -race -tags=integration ./...

# Run tests with a coverage profile written to coverage.out
[group('test')]
test-coverage:
    @go test -race -coverprofile={{ coverage_out }} ./...

# Fail if any internal/ package is under the coverage floor (needs coverage.out)
[group('test')]
coverage-gate:
    #!/usr/bin/env bash
    # Reads an existing coverage.out (run `just test-coverage` first) so CI does
    # not pay for a second test run. Only internal/ is gated: cmd/ is a thin
    # main that .codecov.yml already ignores.
    set -euo pipefail
    if [ ! -f {{ coverage_out }} ]; then
      echo "✗ {{ coverage_out }} not found — run 'just test-coverage' first" >&2
      exit 1
    fi
    awk -v floor="{{ coverage_floor }}" -v prefix="{{ go_package }}/internal/" '
      NR == 1 { next }                     # skip the "mode:" header
      {
        split($1, loc, ":")                # pkg/path/file.go:12.3,14.4
        n = split(loc[1], part, "/")
        pkg = part[1]
        for (i = 2; i < n; i++) pkg = pkg "/" part[i]
        total[pkg] += $2                   # statements in this block
        if ($3 > 0) hit[pkg] += $2         # ...that were executed
      }
      END {
        gated = 0; failed = 0
        for (pkg in total) {
          if (index(pkg, prefix) != 1) continue
          gated++
          pct = total[pkg] > 0 ? 100 * hit[pkg] / total[pkg] : 100
          if (pct + 0.0001 < floor) {
            printf "  ✗ %s: %.1f%% (floor %d%%)\n", pkg, pct, floor
            failed = 1
          } else {
            printf "  ✓ %s: %.1f%%\n", pkg, pct
          }
        }
        if (gated == 0) print "  · no internal/ packages to gate yet"
        exit failed
      }
    ' {{ coverage_out }}
    echo "✓ Coverage gate passed (floor {{ coverage_floor }}%)"

# Run tests and open the HTML coverage report
[group('test')]
test-report:
    @go test -coverprofile={{ coverage_out }} ./...
    @go tool cover -html={{ coverage_out }}

# ─── Lint ───────────────────────────────────────────────────────────

# Run every linter: Go, YAML, Markdown, GitHub Actions
[group('lint')]
lint: lint-go lint-yaml lint-md lint-actions
    @echo "✓ All linters passed"

# Run golangci-lint
[group('lint')]
lint-go:
    @golangci-lint run ./...

# Run golangci-lint with --fix
[group('lint')]
lint-fix:
    @golangci-lint run --fix ./...

# Verify the golangci-lint configuration
[group('lint')]
lint-config:
    @golangci-lint config verify

# Lint YAML files
[group('lint')]
lint-yaml:
    @yamllint .

# Lint Markdown (markdownlint + prettier formatting check)
[group('lint')]
lint-md:
    @markdownlint-cli2 "**/*.md"
    @prettier --check "**/*.md"

# Lint GitHub Actions workflows
[group('lint')]
lint-actions:
    @actionlint

# ─── Format ─────────────────────────────────────────────────────────

# Format everything: Go, YAML, Markdown
[group('lint')]
fmt: fmt-go fmt-yaml fmt-md
    @echo "✓ Formatted"

# Format Go code with the formatters configured in .golangci.yml
[group('lint')]
fmt-go:
    # gofmt, goimports, gci, gofumpt, golines — driven by .golangci.yml so
    # `just fmt-go` and `just lint-go` can never disagree about formatting.
    @golangci-lint fmt ./...

# Format YAML files
[group('lint')]
fmt-yaml:
    @yamlfmt .

# Format Markdown (CHANGELOG.md is excluded via .prettierignore)
[group('lint')]
fmt-md:
    @prettier --write "**/*.md"

# ─── Security & compliance ──────────────────────────────────────────

# Scan for known vulnerabilities in the dependency tree
[group('security')]
govulncheck:
    @govulncheck ./...

# Check dependency licenses against the allow list
[group('security')]
license-check:
    @go-licenses check ./... --allowed_licenses={{ allowed_licenses }}

# Generate CSV report of all dependency licenses
[group('security')]
license-report:
    @go-licenses report ./... --template=.github/licenses-csv.tpl

# ─── Changelog ──────────────────────────────────────────────────────

# Regenerate CHANGELOG.md from conventional commits (never hand-edit it)
[group('changelog')]
changelog:
    @git-cliff -o CHANGELOG.md
    @echo "✓ CHANGELOG.md regenerated"

# Mirror of CI's drift check: fail if CHANGELOG.md is stale
[group('changelog')]
changelog-check:
    #!/usr/bin/env bash
    set -euo pipefail
    trap 'rm -f CHANGELOG.check.md' EXIT
    git-cliff -o CHANGELOG.check.md 2>/dev/null
    if ! diff -q CHANGELOG.md CHANGELOG.check.md >/dev/null; then
      echo "✗ CHANGELOG.md is stale — run 'just changelog' and commit" >&2
      diff CHANGELOG.md CHANGELOG.check.md || true
      exit 1
    fi
    echo "✓ CHANGELOG.md is up to date"

# ─── Repo admin ─────────────────────────────────────────────────────

# Sync GitHub labels from labeler.yml + pr-labels.yml: just labels --dry-run
[group('repo')]
labels *ARGS:
    @./scripts/labels.sh {{ ARGS }}

# ─── Release ────────────────────────────────────────────────────────

# Validate the goreleaser config
[group('release')]
release-check:
    @goreleaser check

# Snapshot release locally (no publish, no sign)
[group('release')]
release-local:
    @goreleaser release --snapshot --clean --skip=publish --skip=sign

# Tag and push a new release: just release v0.1.0
[group('release')]
release tag:
    @git tag -a {{ tag }} -m "Release {{ tag }}"
    @git push origin {{ tag }}

# ─── Composite gates ────────────────────────────────────────────────

# Pre-commit gate: lint + test
[group('gate')]
check: lint test
    @echo "✓ Pre-commit checks passed"

# Full CI gate: everything CI runs, locally
[group('gate')]
ci: lint test-coverage coverage-gate build govulncheck license-check changelog-check
    @echo "✓ CI pipeline complete"
