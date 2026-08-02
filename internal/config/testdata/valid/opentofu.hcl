# Non-hashicorp registries work identically — the label is the full
# source address, which maps 1:1 to the mirror path layout.
provider "registry.opentofu.org/hashicorp/null" {
  versions = ["3.2.4"]
}
