# sluice

Declarative management of an S3-backed Terraform provider network mirror. Humans
write HCL describing which providers and versions are approved; `sluice`
reconciles that against the mirror and publishes the protocol artifacts
Terraform actually reads. Nothing flows except through the gate.

This is the working spec: HCL schema, command surface, and behavior. It is the
starting point for the real repo (scaffolded from the forge registry template)
and should move into `docs/` there.

---

## Mental model

- **Desired state** — HCL files (a directory or a single file) declaring
  providers and their approved versions.
- **Actual state** — the mirror bucket's own protocol files (`index.json`,
  `<version>.json`). There is no state file and no database. What Terraform can
  see _is_ the record.
- **plan** — diff desired vs actual. **apply** — make actual match desired,
  verifying everything cryptographically at ingest.
- Humans never touch JSON; JSON never appears in a PR.

---

## HCL schema

### Minimal single file

```hcl
mirror {
  bucket    = "org-tf-mirror"
  region    = "us-east-1"
  platforms = ["linux_amd64"]
}

provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0"]
}
```

### Full example

```hcl
mirror {
  bucket    = "org-tf-mirror"
  region    = "us-east-1"
  platforms = ["linux_amd64", "darwin_arm64"]  # default matrix
}

provider "registry.terraform.io/hashicorp/aws" {
  versions = [
    "6.2.0",
    "6.3.0",
  ]
}

provider "registry.terraform.io/cloudflare/cloudflare" {
  versions  = ["5.4.0"]
  platforms = ["linux_amd64"]  # override: CI-only provider
}

# Non-hashicorp registries work identically — the label is the full
# source address, which maps 1:1 to the mirror path layout.
provider "registry.opentofu.org/hashicorp/null" {
  versions = ["3.2.4"]
}
```

### Directory layout

Files in the config directory are merged as HCL bodies. Recommended: one file
per namespace or team-owned group, `mirror.hcl` for the `mirror` block.

```text
approved-providers/
  mirror.hcl        # mirror {} only
  hashicorp.hcl     # the big ones
  cloudflare.hcl
  datadog.hcl
```

### Yanking a version

Removal is an HCL edit, reviewed like any other change:

```diff
 provider "registry.terraform.io/hashicorp/aws" {
   versions = [
-    "6.2.0",   # CVE-2026-XXXX — yanked, see SEC-1234
     "6.3.0",
   ]
 }
```

`plan` shows the removal; `apply` rewrites `index.json` without it and deletes
`6.2.0.json`. Zips stay in the bucket (versioning + deny-delete) for forensics.

### Schema reference

**`mirror` block** — exactly one across all files.

| Attribute           | Type         | Required | Notes                                                                |
| ------------------- | ------------ | -------- | -------------------------------------------------------------------- |
| `bucket`            | string       | yes      | Mirror bucket name                                                   |
| `region`            | string       | yes      | Bucket region (builds the REST endpoint URL)                         |
| `platforms`         | list(string) | yes      | Default `os_arch` matrix for all providers                           |
| `signing_key_files` | list(string) | no       | Armored key exports that refresh a registry's stale copy — see below |
| `endpoint`          | string       | no       | S3 API base URL for S3-compatible backends (empty = AWS)             |
| `path_style`        | bool         | no       | Force path-style addressing; required when `endpoint` is set         |

**`provider` block** — zero or more; label is the full source address.

| Attribute                   | Type         | Required | Notes                                                                 |
| --------------------------- | ------------ | -------- | --------------------------------------------------------------------- |
| (label)                     | string       | yes      | `hostname/namespace/type`, e.g. `registry.terraform.io/hashicorp/aws` |
| `versions`                  | list(string) | yes      | Exact versions only — never constraints (`~>` is a validation error)  |
| `platforms`                 | list(string) | no       | Overrides `mirror.platforms` for this provider                        |
| `allow_expired_signing_key` | bool         | no       | Accept a signature whose key has since expired — see below            |

### Upstream signing keys

Ingest verifies `SHA256SUMS` against the GPG keys the origin registry publishes
in its download metadata, and nothing else. Two real-world wrinkles have
explicit handling:

**Stale key exports (`mirror.signing_key_files`).** A registry embeds a copy of
the signing key as it stood when a provider version was published, and does not
re-cut that copy when the owner later extends the key. `registry.terraform.io`
serves an export of HashiCorp's key `34365D9472D7468F` that expired 2026-04-18,
while HashiCorp's own export of the **same fingerprint** runs to 2030-03-01 —
extended, not rotated, in February 2026.

Point `signing_key_files` at the current export and sluice verifies against it:

```hcl
mirror {
  bucket            = "org-tf-mirror"
  region            = "us-east-1"
  platforms         = ["linux_amd64", "darwin_arm64"]
  signing_key_files = ["keys/hashicorp.asc"] # from hashicorp.com/.well-known/pgp-key.txt
}
```

A refreshed export replaces the registry's copy only when the **full primary
fingerprint matches**, so it can never introduce a key the registry did not
publish — it updates validity metadata for a key already trusted, and nothing
about verification is relaxed. An unreadable or unparseable file is a hard
error, never a silent fall back to the stale copy.

Substitution may only ever _add_ validity, never subtract it. If the registry
publishes a key as **revoked**, sluice refuses that provider outright rather
than replacing it — otherwise an export predating the revocation would un-revoke
a compromised key, which is the one thing revocation exists to stop. The list is
mirror-wide and applied to every fetch, so an export for one registry's key
harmlessly matches nothing while fetching from another.

**`allow_expired_signing_key`.** The escape hatch for a genuinely expired key
with no refreshed export available. It accepts a signature made while the key
was valid, and only that: a **revoked** key is still refused (revocation is
re-checked against the present, not the signing time), as is a bad signature, an
unknown signer, or a signature claiming a future creation time. Every artifact
it covers logs a `WARN` naming the key. Prefer `signing_key_files` — an extended
key verifies strictly.

### Validation rules

- Exactly one `mirror` block; duplicate `provider` labels across files are an
  error (merge is explicit, never silent union).
- Labels must parse as registry source addresses: three segments, valid
  hostname.
- Versions must be valid semver, unique within a block. Constraint syntax is
  rejected with a pointer to why (sluice publishes exact sets; it never
  resolves).
- Platforms must match `os_arch` from the known matrix (`linux_amd64`,
  `darwin_arm64`, ...).
- Empty `versions` list is an error — removing a provider entirely means
  deleting its block (plan then shows all versions as removals).

---

## Commands

```text
sluice validate  [--config-dir DIR | --config-file FILE] [--s3-endpoint URL] [--s3-path-style]
sluice plan      [--config-dir DIR | --config-file FILE] [--json] [--detailed-exitcode] [--verbose] [--s3-endpoint URL] [--s3-path-style]
sluice apply     [--config-dir DIR | --config-file FILE] [--auto-approve] [--json]
                 [--cosign-key REF] [--authorizing-commit SHA] [--allow-unversioned-backend] [--s3-endpoint URL] [--s3-path-style]
sluice export    [--config-dir DIR | --config-file FILE] [--out FILE] [--s3-endpoint URL] [--s3-path-style]
sluice bootstrap PATH... [--out FILE]
```

Backend selection (DESIGN-0005): explicit flags beat `SLUICE_S3_ENDPOINT` /
`SLUICE_S3_PATH_STYLE`, which beat the manifest. An endpoint with path-style
unset at every layer is an error; non-TLS endpoints are refused outside loopback
hosts.

### `validate`

Parse + schema-check only. No network. Exit 0/1.

### `plan`

Reads every declared provider's `index.json` and `<version>.json` from the
bucket, computes the diff, prints it. Never writes.

Exit codes: `0` no changes · `1` error · `2` changes present
(`--detailed-exitcode`; without the flag, 0 covers both clean and diff).

Human output:

```text
sluice plan

registry.terraform.io/hashicorp/aws
  + 6.3.0  [linux_amd64, darwin_arm64]
  - 6.1.0  [linux_amd64, darwin_arm64]     (yank)

registry.terraform.io/cloudflare/cloudflare
  ~ 5.4.0  +darwin_arm64                   (platform add)

Plan: 1 to add, 1 to remove, 1 platform change.
```

`--json` output (stable contract for the PR comment bot):

```json
{
  "add": [
    {
      "provider": "registry.terraform.io/hashicorp/aws",
      "version": "6.3.0",
      "platforms": ["linux_amd64", "darwin_arm64"]
    }
  ],
  "remove": [
    { "provider": "registry.terraform.io/hashicorp/aws", "version": "6.1.0" }
  ],
  "add_platform": [
    {
      "provider": "registry.terraform.io/cloudflare/cloudflare",
      "version": "5.4.0",
      "platforms": ["darwin_arm64"]
    }
  ]
}
```

### `apply`

Plan, confirm (interactive) or proceed (`--auto-approve`), execute. Per added
version, per platform:

1. Resolve download metadata from the origin registry API
   (`/v1/providers/{ns}/{type}/{version}/download/{os}/{arch}`): zip URL,
   `SHA256SUMS`, `SHA256SUMS.sig`, publisher signing keys.
2. Verify the GPG signature over `SHA256SUMS` against the registry-published
   keys. Fail → abort, nothing published.
3. Download the zip; verify SHA-256 against the signed sums.
4. Compute the mirror hash: `dirhash.HashZip` from
   `golang.org/x/mod/sumdb/dirhash` — produces the exact `h1:` value Terraform
   validates.
5. Stage the `<version>.json` entry.
6. Sign: `cosign sign-blob` over the zip — keyless via the CI OIDC identity (or
   a KMS key) — uploading `<zip>.sig` plus an in-toto attestation alongside:
   provider, version, platform, SHA-256, `h1:`, upstream signing key ID, and the
   approved-providers commit that authorized it.

Publish per provider, strictly ordered: **zips → `<version>.json` files →
`index.json` last**, with an ETag-conditional write on `index.json`. There is no
plan file: `apply` re-reads the bucket and re-computes the diff itself, so the
ETag it conditions on is the one it just read, and the read→confirm→write window
it guards is its own. Index is the atomic publish; a precondition failure means
concurrent modification → abort with exit 3 and a message to re-plan.

Degraded backends (DESIGN-0005: no versioning or ignored preconditions, as
detected by the startup probe) refuse `apply` pre-mutation with exit 1 unless
`--allow-unversioned-backend` is given; opted-in applies warn once in the audit
trail and retire yanked artifacts under `_retired/` instead of versioning them.

Signing identity comes from `--cosign-key` (a KMS URI or key file; empty means
keyless OIDC) and `--authorizing-commit` (defaults to `$GITHUB_SHA`). There is
no flag to skip signing. `cosign` 2.4 or newer must be on `PATH`; `apply`
preflights it before any fetch or write, so a missing or too-old binary fails
before anything is touched.

Removals: rewrite `index.json`, delete `<version>.json`, leave zips.

Every publish/retract emits one structured log line (the ingest audit trail):

```json
{
  "action": "publish",
  "provider": "registry.terraform.io/hashicorp/aws",
  "version": "6.3.0",
  "platform": "linux_amd64",
  "h1": "h1:...",
  "sha256": "...",
  "signing_key_id": "34365D9472D7468F",
  "s3_version_id": "..."
}
```

### `export`

Emits the canonical JSON projection of desired state — no network, no side
effects, just the parsed manifest:

```json
{ "providers": { "registry.terraform.io/hashicorp/aws": ["6.2.0", "6.3.0"] } }
```

This is the bridge to the policy layer: the policy repo commits the output as
`data/providers.json`, so the conftest allowlist rules consume exactly what the
mirror serves, produced by the same parser that publishes it. The rego stays
static — logic is hand-written and tested; only data is generated. The output
schema is a contract: stable field names, sorted keys and versions,
deterministic bytes for `--check`-style diffing.

### `bootstrap`

One-time rollout helper. Walks PATH(s) for `.terraform.lock.hcl` files and emits
seed HCL covering every provider/version currently in use, grouped by namespace,
sorted, deduplicated. Output goes to stdout or `--out`. Constraint info in lock
files is ignored — lock files record exact versions, which is exactly what
sluice wants.

---

## Mirror layout (what apply produces)

```text
registry.terraform.io/
  hashicorp/
    aws/
      index.json                                   {"versions":{"6.2.0":{},"6.3.0":{}}}
      6.3.0.json                                   {"archives":{"linux_amd64":{"url":"terraform-provider-aws_6.3.0_linux_amd64.zip","hashes":["h1:..."]}}}
      terraform-provider-aws_6.3.0_linux_amd64.zip
```

Consumed by Terraform via:

```hcl
provider_installation {
  network_mirror {
    url = "https://org-tf-mirror.s3.us-east-1.amazonaws.com/"
  }
}
```

---

## Behavior details

- **AWS auth:** default credential chain; CI uses the publisher OIDC role
  (Put/Get/List, no delete). `plan` needs read-only.
- **Idempotency:** re-running a failed apply republishes staged artifacts;
  because the index is written last, consumers never observed the partial state
  and the retry converges.
- **Concurrency:** single-writer by CI convention, enforced by the conditional
  index writes — a second writer loses cleanly.
- **Network:** origin registries (HTTPS) + the bucket. No other endpoints.
  Registry downtime fails apply but never affects serving.
- **Determinism:** identical HCL + identical bucket state ⇒ empty plan. Drift
  (out-of-band bucket edits) surfaces as a non-empty plan against unchanged HCL
  — always an incident signal.

## Yank runbook

1. Comment out (or delete) the version in the provider file with the CVE/ticket
   reference; PR with security review.
2. Merge. CI `apply` rewrites the index and deletes the version JSON — new
   installs are gone everywhere. The same commit regenerates the policy
   allowlist data, cutting a policy bundle release.
3. The policy gate (Atlantis Conftest stage, repo-guardian) now denies any plan
   whose lock file pins the yanked version. This is the immediate control:
   already-initialized workspaces never re-contact the mirror, so the gate — not
   the mirror — stops them on their next run.
4. Cache invalidation — owned by the Atlantis deployment, not sluice: the
   Argo/Kargo-synced step wipes the Terragrunt cache (or a rollout restart, if
   the cache moves to emptyDir) and the cache server rewarms from the mirror,
   which no longer serves the version.
5. Renovate's mirror datasource stops listing it and PRs consumers forward;
   stragglers stay blocked at the gate until they move.

## Exit codes

| Code | Meaning                                       |
| ---- | --------------------------------------------- |
| 0    | Success / no changes                          |
| 1    | Error (validation, network, verification, S3) |
| 2    | `plan --detailed-exitcode`: changes present   |
| 3    | `apply`: conditional-write conflict — re-plan |

---

## Suggested package layout

For the template scaffold; adjust to taste.

```text
cmd/sluice/          # main, command wiring, flag parsing, exit codes
internal/config/     # HCL schema types + decode/validate (hclkit)
internal/mirror/     # protocol types, index read/parse, diff engine
internal/registry/   # origin registry client, download, GPG + sums verify
internal/hash/       # h1 via dirhash.HashZip (thin, golden-tested)
internal/publish/    # S3 publisher: ordering, conditional writes, retract
internal/bootstrap/  # lock file walker → seed HCL
```

Notes that matter regardless of layout: the diff engine is pure (struct-in,
struct-out, table-driven tests); registry and S3 sit behind small interfaces so
`httptest` fakes and LocalStack cover them; errors wrap with `%w` and carry the
(provider, version, platform) tuple; the verification path has tampered-fixture
tests proving it fails closed.

## Decisions

- **OpenTofu: day one.** The registry client is protocol-generic;
  `registry.opentofu.org` addresses are first-class, with a canary provider from
  that registry in the e2e tests to pin the API surface.
- **cosign: yes.** Every published artifact carries a signature and in-toto
  attestation (see `apply` step 6). Verification is part of the compliance
  evidence trail, not the Terraform install path — terraform keeps validating
  `h1:`; auditors and incident review get the attestation chain.

## Open items

- Per-provider platform overrides: resolved — kept (see DESIGN-0001 Open
  Questions).
