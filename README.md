# sluice

Declarative management of an S3-backed Terraform provider network mirror.

You write HCL declaring which providers and versions are approved. `sluice`
diffs that against the mirror bucket, fetches anything missing from the origin
registry, verifies it cryptographically, signs it, and publishes the protocol
files `terraform init` and `tofu init` actually read. Nothing enters the mirror
except through that gate.

There is no state file. The mirror's own `index.json` and `<version>.json` are
the record — what Terraform can see _is_ the state.

## Quickstart

```sh
mise install                  # pinned toolchain (go, golangci-lint, cosign, ...)
just                          # task menu
just build                    # binary at build/bin/sluice
```

Declare what you approve:

```hcl
# approved-providers/mirror.hcl
mirror {
  bucket    = "org-tf-mirror"
  region    = "us-east-1"
  platforms = ["linux_amd64", "darwin_arm64"]
}

# approved-providers/hashicorp.hcl
provider "registry.terraform.io/hashicorp/null" {
  versions = ["3.2.4"]
}
```

Then check it, preview it, and publish it:

```sh
sluice validate --config-dir approved-providers   # parse + schema only, no network
sluice plan     --config-dir approved-providers   # read the bucket, print the diff
sluice apply    --config-dir approved-providers   # fetch, verify, sign, publish
```

`plan` never writes. `apply` re-plans inside its own conditional-write window,
prompts for confirmation, then publishes in order — archives first, `index.json`
last, so the mirror is never observable in a half-published state.

Point a consumer at the result with a `network_mirror` block and nothing else:

```hcl
# ~/.terraformrc
provider_installation {
  network_mirror {
    url = "https://org-tf-mirror.s3.us-east-1.amazonaws.com/"
  }
}
```

Two more commands cover the edges:

```sh
sluice export    --config-dir approved-providers   # canonical JSON for policy tooling
sluice bootstrap ./repos --out approved.hcl        # seed HCL from existing lock files
```

### Exit codes

| Code | Meaning                                                        |
| ---- | -------------------------------------------------------------- |
| 0    | Success, or `plan` with no changes                             |
| 1    | Error                                                          |
| 2    | `plan --detailed-exitcode` found changes                       |
| 3    | Conditional-write conflict — someone else wrote; re-run `plan` |

## What gets verified

Every artifact is checked before it is staged, and staged before it is
published:

1. Download metadata resolved from the origin registry's v1 API.
2. `SHA256SUMS` verified against its detached GPG signature, using only the keys
   that registry published — plus any refreshed export of the same fingerprint
   (see `mirror.signing_key_files` in [the spec](docs/sluice-spec.md)).
3. The zip streamed under a size bound, SHA-256 computed during the stream and
   compared to the signed sums entry.
4. `h1:` dirhash computed for the version document.
5. cosign signature and an in-toto attestation recording the provenance.

Any failure leaves the staging directory untouched. Every publish and retract
emits one structured audit line naming the provider, version, platform, hashes,
upstream signing key, and S3 version ID.

## Development

```sh
just test              # unit tests, race detector
just test-integration  # LocalStack suite (needs LocalStack on :4566)
just e2e terraform     # init oracle: apply to LocalStack, then real terraform init
just e2e tofu          # same, against OpenTofu
just lint              # every linter
just ci                # everything CI runs
```

`just e2e` is the load-bearing test: it builds a real mirror from live registry
fetches, then runs `init` in a container whose only provider source is that
mirror. A green run proves the whole chain end to end.

## Layout

```text
cmd/sluice/           main package — thin; wires commands
internal/             library code (private to this module)
docs/                 rfc/ adr/ design/ impl/ investigation/
docs/sluice-spec.md   HCL schema, commands, exit codes
scripts/e2e.sh        the init oracle
.goreleaser.yml       release config (multi-arch archives + SBOMs)
mise.toml             pinned toolchain
justfile              task runner
```

## Release

```sh
just release v0.1.0    # tag + push; CI runs goreleaser
```

Merging to `main` auto-releases based on the PR's semver label. Multi-arch
archives, checksums, and syft SBOMs are published GPG-signed. The CLI ships as
archives only — there is no container image. Version metadata (`version`,
`commit`, `date`) is embedded via `-ldflags` and printed by `sluice --version`.

## Conventions

See `CLAUDE.md` for the full operating notes (Go-specific + homelab universals).

## License

Apache-2.0
