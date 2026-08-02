mirror {
  bucket    = "test-mirror"
  region    = "us-east-1"
  platforms = ["linux_amd64", "darwin_arm64"]
}

provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.2.0", "6.3.0"]
}

provider "registry.terraform.io/cloudflare/cloudflare" {
  versions = ["5.4.0"]
}
