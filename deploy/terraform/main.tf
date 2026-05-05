// Backward-compat root: callers using
//
//   module "polystac" {
//     source       = "github.com/example/polystac//deploy/terraform"
//     backend      = "pgstac"
//     function_zip = "polystac-lambda.zip"
//     pg_dsn       = "postgresql://..."
//   }
//
// continue to work unchanged. This file is a thin passthrough to
// modules/lambda. New deploys should pick a composition from
// deploy/terraform/examples/ or use modules/{lambda,server,pgstac,
// opensearch} directly. See docs/deploy-aws.md.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = { source = "hashicorp/aws", version = ">= 5.0" }
  }
}

module "lambda" {
  source = "./modules/lambda"

  name                = var.name
  function_zip        = var.function_zip
  backend             = var.backend
  memory_mb           = var.memory_mb
  timeout_s           = var.timeout_s
  architecture        = var.architecture
  log_level           = var.log_level
  log_retention_days  = var.log_retention_days
  landing_title       = var.landing_title
  landing_description = var.landing_description

  pg_dsn                 = var.pg_dsn
  pg_pool_min            = var.pg_pool_min
  pg_pool_max            = var.pg_pool_max
  pg_use_api_hydrate     = var.pg_use_api_hydrate
  vpc_subnet_ids         = var.vpc_subnet_ids
  vpc_security_group_ids = var.vpc_security_group_ids

  es_hosts             = var.es_hosts
  es_username          = var.es_username
  es_password          = var.es_password
  es_verify_certs      = var.es_verify_certs
  es_refresh           = var.es_refresh
  es_index_prefix      = var.es_index_prefix
  es_collections_index = var.es_collections_index

  extra_policy_arns = var.extra_policy_arns
}
