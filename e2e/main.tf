# The e2e canaries: one provider per registry, both installed from the
# sluice-populated mirror. Fully-qualified sources so terraform and tofu
# resolve identical addresses regardless of their default registry.
terraform {
  required_providers {
    null = {
      source  = "registry.terraform.io/hashicorp/null"
      version = "3.2.4"
    }
    tofunull = {
      source  = "registry.opentofu.org/hashicorp/null"
      version = "3.2.4"
    }
  }
}
