#!/usr/bin/env bash
# e2e.sh - End-to-end init oracle against a LocalStack-backed mirror.
#
# Builds a real mirror with `sluice apply` (live registry fetches, GPG
# verification, cosign signing) against LocalStack S3, then runs
# `terraform init` or `tofu init` in a container whose ONLY provider
# installation method is that mirror. A green init proves the mirror
# speaks the Provider Network Mirror Protocol end to end.
#
# Usage:
#   ./scripts/e2e.sh [terraform|tofu]
#
# Requirements: docker, curl, openssl, cosign 2.x (mise provides it),
# a built build/bin/sluice (run `just build`), and LocalStack on
# SLUICE_LOCALSTACK_ENDPOINT (default http://localhost:4566).
#
# Both tools require https network_mirror URLs, so the container talks
# to LocalStack's TLS listener as localhost.localstack.cloud (a SAN on
# its self-signed cert) via --add-host host-gateway, trusting the cert
# through SSL_CERT_FILE.

set -euo pipefail

TOOL="${1:-terraform}"
ENDPOINT="${SLUICE_LOCALSTACK_ENDPOINT:-http://localhost:4566}"
# Virtual-host-style S3 endpoint for the sluice binary; the *.s3
# wildcard resolves to 127.0.0.1 via LocalStack's public DNS.
S3_ENDPOINT="${SLUICE_E2E_S3_ENDPOINT:-http://s3.localhost.localstack.cloud:4566}"
TERRAFORM_IMAGE="${TERRAFORM_IMAGE:-hashicorp/terraform:1.14.7}"
TOFU_IMAGE="${TOFU_IMAGE:-ghcr.io/opentofu/opentofu:1.11.5}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SLUICE_BIN="${SLUICE_BIN:-${REPO_ROOT}/build/bin/sluice}"

log() { printf '\033[0;34me2e>\033[0m %s\n' "$*"; }
die() {
  printf '\033[0;31me2e>\033[0m %s\n' "$*" >&2
  exit 1
}

case "${TOOL}" in
  terraform) IMAGE="${TERRAFORM_IMAGE}" ;;
  tofu) IMAGE="${TOFU_IMAGE}" ;;
  *) die "unknown tool '${TOOL}' (expected terraform or tofu)" ;;
esac

for dep in docker curl openssl cosign aws; do
  command -v "${dep}" >/dev/null || die "missing dependency: ${dep}"
done
[[ -x "${SLUICE_BIN}" ]] || die "${SLUICE_BIN} not built — run 'just build' first"

curl -sf "${ENDPOINT}/_localstack/health" >/dev/null \
  || die "LocalStack not reachable at ${ENDPOINT}"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "${WORKDIR}" 2>/dev/null || true' EXIT

BUCKET="sluice-e2e-$(date +%s)"
HOST_PORT="${ENDPOINT#http://}"

# --- Provision the bucket --------------------------------------------
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

log "creating versioned bucket ${BUCKET}"
aws --endpoint-url "${ENDPOINT}" s3api create-bucket --bucket "${BUCKET}" >/dev/null
aws --endpoint-url "${ENDPOINT}" s3api put-bucket-versioning \
  --bucket "${BUCKET}" --versioning-configuration Status=Enabled

# --- Signing key -----------------------------------------------------
log "generating cosign keypair"
export COSIGN_PASSWORD="${COSIGN_PASSWORD:-sluice-e2e}"
(cd "${WORKDIR}" && cosign generate-key-pair >/dev/null 2>&1)

# --- Upstream signing keys -------------------------------------------
# registry.terraform.io embeds the copy of HashiCorp's key that was
# current when each provider was published, and does not re-cut it when
# the key is extended. That copy expired 2026-04-18; the same
# fingerprint runs to 2030 in HashiCorp's own export, so verify against
# the canonical one instead.
#
# The committed copy is used rather than fetching hashicorp.com on
# every run: it keeps this job off a third-party network dependency,
# and TestHashiCorpKeyIsExtendedUpstream fails if it ever goes stale.
# Set SLUICE_E2E_FETCH_KEY=1 to pull the live key instead.
KEY_FILE="${REPO_ROOT}/internal/registry/testdata/hashicorp-extended-key.asc"
if [[ -n "${SLUICE_E2E_FETCH_KEY:-}" ]]; then
  log "fetching HashiCorp's current signing key"
  KEY_FILE="${WORKDIR}/hashicorp.asc"
  curl -sf --max-time 30 "https://www.hashicorp.com/.well-known/pgp-key.txt" \
    -o "${KEY_FILE}" || die "could not fetch HashiCorp's signing key"
else
  log "using the committed HashiCorp signing key"
  [[ -f "${KEY_FILE}" ]] || die "missing ${KEY_FILE}"
fi

# --- Manifest: both registries, the platforms CI and dev machines run
cat >"${WORKDIR}/manifest.hcl" <<EOF
mirror {
  bucket             = "${BUCKET}"
  region             = "us-east-1"
  platforms          = ["linux_amd64", "linux_arm64"]
  signing_key_files  = ["${KEY_FILE}"]
}

provider "registry.terraform.io/hashicorp/null" {
  versions = ["3.2.4"]
}

provider "registry.opentofu.org/hashicorp/null" {
  versions = ["3.2.4"]
}
EOF

# --- Populate the mirror with the real binary ------------------------
log "running sluice apply (live registry fetch + verify + sign)"
AWS_ENDPOINT_URL_S3="${S3_ENDPOINT}" \
  "${SLUICE_BIN}" apply \
  --config-file "${WORKDIR}/manifest.hcl" \
  --auto-approve \
  --cosign-key "${WORKDIR}/cosign.key" \
  --authorizing-commit "$(git -C "${REPO_ROOT}" rev-parse HEAD)"

aws --endpoint-url "${ENDPOINT}" s3 cp \
  "s3://${BUCKET}/registry.terraform.io/hashicorp/null/index.json" - \
  | grep -q '3.2.4' || die "mirror index missing after apply"

# --- Trust LocalStack's self-signed cert -----------------------------
openssl s_client -connect "${HOST_PORT}" -servername localhost.localstack.cloud \
  </dev/null 2>/dev/null \
  | openssl x509 -outform PEM >"${WORKDIR}/localstack.pem"

# Only the network_mirror block: no direct fallback, so every provider
# MUST come from the mirror or init fails.
cat >"${WORKDIR}/mirror.tfrc" <<EOF
provider_installation {
  network_mirror {
    url = "https://localhost.localstack.cloud:4566/${BUCKET}/"
  }
}
EOF

mkdir "${WORKDIR}/tf"
cp "${REPO_ROOT}/e2e/main.tf" "${WORKDIR}/tf/"

# --- The oracle ------------------------------------------------------
log "running ${TOOL} init against the mirror (${IMAGE})"
docker run --rm \
  --add-host localhost.localstack.cloud:host-gateway \
  -v "${WORKDIR}/tf:/work" -w /work \
  -v "${WORKDIR}/mirror.tfrc:/mirror.tfrc:ro" \
  -v "${WORKDIR}/localstack.pem:/localstack.pem:ro" \
  -e TF_CLI_CONFIG_FILE=/mirror.tfrc \
  -e SSL_CERT_FILE=/localstack.pem \
  "${IMAGE}" init

for canary in registry.terraform.io/hashicorp/null registry.opentofu.org/hashicorp/null; do
  [[ -d "${WORKDIR}/tf/.terraform/providers/${canary}/3.2.4" ]] \
    || die "${canary} was not installed from the mirror"
done
grep -q 'h1:' "${WORKDIR}/tf/.terraform.lock.hcl" \
  || die "lock file missing h1 hashes"

log "OK: ${TOOL} installed both canaries exclusively from the mirror"
