// Example: PolyStac Lambda + managed AWS OpenSearch domain.
//
// Provisions:
//   - OpenSearch Service domain with FGAC + admin internal user
//     (password generated, stored in Secrets Manager).
//   - Lambda function (modules/lambda), no VPC needed since the
//     OS domain is reachable over its public endpoint.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws    = { source = "hashicorp/aws", version = ">= 5.0" }
    random = { source = "hashicorp/random", version = ">= 3.0" }
  }
}

provider "aws" {
  region = var.region
}

variable "region" {
  type    = string
  default = "us-east-1"
}

variable "function_zip" {
  type = string
}

variable "domain_name" {
  type    = string
  default = "polystac"
}

module "opensearch" {
  source = "../../modules/opensearch"
  name   = var.domain_name
}

module "polystac" {
  source       = "../../modules/lambda"
  name         = "polystac"
  function_zip = var.function_zip
  backend      = "opensearch"

  es_hosts    = module.opensearch.endpoint
  es_username = module.opensearch.admin_username
  es_password = module.opensearch.admin_password
  es_refresh  = "false"
}

output "function_url" {
  value = module.polystac.function_url
}

output "opensearch_endpoint" {
  value = module.opensearch.endpoint
}

output "opensearch_secret_arn" {
  value = module.opensearch.secret_arn
}
