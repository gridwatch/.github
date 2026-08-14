# The shared tflint configuration for the fleet. Rendered into `.github`, which
# the Super-Linter workflow clones without credentials, and into `tooling`,
# which `task lint-tflint` reads from a sibling checkout. One template, so the
# check that runs in CI and the one that runs locally cannot diverge.
#
# Super-Linter ships its own config enabling the aws, azurerm and google
# rulesets. Pointing `TERRAFORM_TFLINT_CONFIG_FILE` at this one replaces it, so
# the fleet controls which rulesets run and at which version rather than
# inheriting whatever the image happens to pin.

config {
  call_module_type = "none"
}

# see https://github.com/terraform-linters/tflint-ruleset-terraform
plugin "terraform" {
  enabled = true
  preset  = "recommended"
}

# Enabled everywhere rather than only for repositories holding azurerm
# resources: the ruleset is inert where there are none, and a single shared
# config is what keeps CI and local linting identical.
#
# see https://github.com/terraform-linters/tflint-ruleset-azurerm/releases
plugin "azurerm" {
  enabled = true

  source  = "github.com/terraform-linters/tflint-ruleset-azurerm"
  version = "0.31.1"
}
