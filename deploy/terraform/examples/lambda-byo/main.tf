// Example: PolyStac on Lambda, bring-your-own datastore.
//
// Use this when you already operate a Postgres+pgstac or OpenSearch
// cluster and just want PolyStac in front of it. For a fully managed
// datastore, see lambda-pgstac/ or lambda-opensearch/.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = { source = "hashicorp/aws", version = ">= 5.0" }
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
  type        = string
  description = "Path to the polystac-lambda zip (built with go build ./cmd/polystac-lambda)."
}

variable "backend" {
  type        = string
  description = "pgstac | opensearch | elasticsearch."
}

variable "pg_dsn" {
  type      = string
  default   = ""
  sensitive = true
}

variable "vpc_subnet_ids" {
  type    = list(string)
  default = []
}

variable "vpc_security_group_ids" {
  type    = list(string)
  default = []
}

variable "es_hosts" {
  type    = string
  default = ""
}

variable "es_username" {
  type      = string
  default   = ""
  sensitive = true
}

variable "es_password" {
  type      = string
  default   = ""
  sensitive = true
}

module "polystac" {
  source = "../../modules/lambda"

  name         = "polystac"
  function_zip = var.function_zip
  backend      = var.backend

  pg_dsn                 = var.pg_dsn
  vpc_subnet_ids         = var.vpc_subnet_ids
  vpc_security_group_ids = var.vpc_security_group_ids

  es_hosts    = var.es_hosts
  es_username = var.es_username
  es_password = var.es_password
}

output "function_url" {
  value = module.polystac.function_url
}
