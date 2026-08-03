# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/).
## [unreleased]

### Features

- *(config)* Add manifest model and curated platform matrix
- *(config)* Add HCL loader with merged-body semantics
- *(config)* Reject duplicate provider labels across merged files
- *(config)* Implement the full semantic validation rule set
- *(cli)* Wire the cobra command tree with validate end to end
- *(config)* Close Phase 1 with style-review fixes and rule-table audit
- *(mirror)* Add protocol types and desired-state expansion
- *(mirror)* Add the pure diff engine with deterministic ordering
- *(export)* Emit the canonical JSON projection of the approved set
- *(bootstrap)* Seed a validating manifest from fleet lock files
- *(hash)* H1 provider zip hashing via dirhash
- *(registry)* Client scaffolding and download metadata resolution
- *(registry)* GPG verification of SHA256SUMS and strict sums parsing
- *(registry)* Streamed size-bounded zip download with SHA-256 verify
- *(registry)* Bounded retry with backoff and clean cancellation
- *(registry)* FetchVerified orchestration and httptest fake registry
- *(publish)* Bucket interface, S3 adapter, and actual-state reader
- *(cli)* Implement sluice plan with json golden and exit contract
- *(publish)* Cosign signer with fail-closed preflight
- *(publish)* Applier — ordered, conditional, idempotent, audited
- *(cli)* Implement sluice apply with confirmation and full wiring

### Bug Fixes

- *(deps)* Bump cloudflare/circl to v1.6.3 for GO-2026-4550
- *(registry)* Close Phase 3 with security and style review hardening
- *(registry)* Decode signing keys from gpg_public_keys, not gpg_keys

### Refactor

- *(cli)* Close Phase 2 with style-review fixes
- Close Phase 4 with style-review fixes

### Documentation

- Green the markdown lint baseline

### Testing

- *(registry)* Tampered-fixture fail-closed suite
- *(publish)* Add LocalStack integration suite behind the integration tag

### Miscellaneous Tasks

- *(dependabot)* Initialize dependabot
- Consolidate markdownlint config, prune stale scaffold refs

