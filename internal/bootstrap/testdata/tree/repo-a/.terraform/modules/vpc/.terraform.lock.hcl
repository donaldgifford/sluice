# Decoy: lock file inside a .terraform directory — a third-party
# module's own lock, not "in use here". The walker must skip it.

provider "registry.terraform.io/hashicorp/never" {
  version = "9.9.9"
}
