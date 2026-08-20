mirror {
  bucket    = "org-tf-mirror"
  region    = "us-east-1"
  platforms = ["linux_amd64", "darwin_arm64"]
}

provider "registry.terraform.io/hashicorp/aws" {
  versions = ["6.3.0"]
}
