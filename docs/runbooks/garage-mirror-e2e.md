# Runbook: Garage-Backed Mirror E2E (Proven 2026-09-16)

Proves the init oracle against Garage: `sluice apply` into a Garage bucket, then
`terraform init` in a container whose only provider source is that mirror,
served through a TLS reverse proxy. First proven end-to-end on 2026-09-16
(Garage v2.3.0, Caddy 2, terraform 1.14 container): null 3.2.1 on three
platforms installed with lock-file h1s matching sluice-published bytes.

## 0. Prerequisites

Docker, `aws` CLI, `cosign` 2.x, terraform or tofu binaries, a built
`build/bin/sluice`. A local (throwaway) cosign keypair stands in for keyless
OIDC, which needs CI:
`cosign generate-key-pair --output-key-prefix /tmp/e2e/cosign` with
`COSIGN_PASSWORD` set.

## 1. Garage (single node)

```bash
# garage.toml: metadata_dir + data_dir, db_engine sqlite,
# replication_factor 1, rpc_secret 64 hex chars,
# [s3_api] s3_region garage, api_bind_addr :3900,
#   root_domain .s3.garage.local,
# [s3_web] bind_addr :3902, root_domain .web.garage.local,
# [admin] api_bind_addr :3903
docker run -d --name garage -p 3900:3900 -p 3902:3902 \
  -v $PWD/garage.toml:/etc/garage.toml \
  -v $PWD/data:/var/lib/garage/data -v $PWD/meta:/var/lib/garage/meta \
  garage:2.3.0
NODE=$(docker exec garage /garage node id | grep -o '[0-9a-f]*@[0-9.]*:[0-9]*' | tail -n 1)
docker exec garage /garage layout assign -z dc1 -c 10G "$NODE"
docker exec garage /garage layout apply --version 1
docker exec garage /garage bucket create sluice-mirror
docker exec garage /garage key create sluice-pub
docker exec garage /garage bucket allow --read --write sluice-mirror --key sluice-pub
docker exec garage /garage key info --show-secret sluice-pub  # record ID + secret
docker exec garage /garage bucket website --allow sluice-mirror
```

Layout assign must show staged changes before `apply --version 1`; if apply
complains about zero capacity, re-run assign against the node ID from
`garage status` — the first attempt can race node discovery.

## 2. Serving proxy (Caddy, TLS mandatory)

Terraform refuses non-HTTPS mirrors
(`mirror: the mirror must be at an https: URL`), so the proxy terminates TLS.
The website endpoint routes by `Host`, not path: hardcode the bucket's website
hostname.

```caddy
https://localhost:8443, https://host.docker.internal:8443 {
	bind 0.0.0.0
	tls internal
	reverse_proxy garage:3902 {
		header_up Host sluice-mirror.web.garage.local
	}
}
```

Run Caddy on the same docker network as Garage, persisting `/data` so the
internal CA survives restarts. Consumers trust
`/data/caddy/pki/authorities/local/root.crt` (e.g. `SSL_CERT_FILE` — note Go
ignores it on macOS, so run the init inside a Linux container).

## 3. Apply to Garage

Manifest uses the S3-API endpoint with path-style addressing; degraded mode is
expected (Garage ignores preconditions), so the flag is explicit:

```hcl
mirror {
  bucket     = "sluice-mirror"
  region     = "garage"
  platforms  = ["linux_amd64", "linux_arm64", "darwin_arm64"]
  endpoint   = "http://localhost:3900"
  path_style = true
}

provider "registry.terraform.io/hashicorp/null" {
  versions = ["3.2.1"]
  # allow_expired_signing_key = true  # only if the upstream key is expired
}
```

```bash
export AWS_ACCESS_KEY_ID=<id> AWS_SECRET_ACCESS_KEY=<secret>
sluice apply --config-file mirror.hcl --auto-approve \
  --allow-unversioned-backend --cosign-key /tmp/e2e/cosign.key
```

Expect: plan renders, degraded WARN in the audit trail, publish lines with
`s3_version_id` populated (Garage returns content-hash IDs — opaque refs, not
true versions). Confirm the layout (`index.json`, `<version>.json`, zips,
`.sig`, `.intoto.jsonl`) via path-style `s3 ls`.

## 4. Init oracle

```bash
# .terraformrc
provider_installation {
  network_mirror {
    url = "https://host.docker.internal:18443/"
  }
}
docker run --rm -v $PWD/tf:/work -v root.crt:/ca.crt:ro \
  -e SSL_CERT_FILE=/ca.crt -e TF_CLI_CONFIG_FILE=/work/.terraformrc \
  -w /work hashicorp/terraform:1.14 init
```

Success is a lock file pinning the mirrored version with sluice-published h1s.
Gotchas encountered: omitting `TF_CLI_CONFIG_FILE` silently inits from the
public registry (always assert the lock version/hashes); the init platform must
be in the manifest matrix (a linux/arm64 container needs `linux_arm64`
published); `terraform` on macOS ignores `SSL_CERT_FILE` (use the container).

## 5. Teardown

`docker rm -f` the containers, `docker network rm`, delete scratch dirs. Probe
keys are self-cleaning; verify the bucket is empty afterwards.
