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

### Refactor

- *(cli)* Close Phase 2 with style-review fixes

### Documentation

- Green the markdown lint baseline

### Miscellaneous Tasks

- *(dependabot)* Initialize dependabot
- Consolidate markdownlint config, prune stale scaffold refs

