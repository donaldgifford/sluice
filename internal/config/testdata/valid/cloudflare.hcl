provider "registry.terraform.io/cloudflare/cloudflare" {
  versions  = ["5.4.0"]
  platforms = ["linux_amd64"] # override: CI-only provider
}
