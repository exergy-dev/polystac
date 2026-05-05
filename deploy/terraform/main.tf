// Terraform module: PolyStac on AWS Lambda. Two backend modes:
//
//   backend = "pgstac"
//     The Lambda joins a VPC and talks to RDS Postgres + pgstac.
//     Set vpc_subnet_ids and vpc_security_group_ids; supply pg_dsn.
//
//   backend = "opensearch" / "elasticsearch"
//     The Lambda runs without VPC and talks to AWS OpenSearch Service
//     over a public endpoint. Supply es_hosts (and es_username/password
//     when using fine-grained access control with internal users).
//
// Build the deployment zip first:
//   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
//     go build -trimpath -ldflags='-s -w' -o bootstrap ./cmd/polystac-lambda
//   zip polystac-lambda.zip bootstrap
//
// Then consume:
//
//   module "polystac" {
//     source       = "github.com/example/polystac//deploy/terraform"
//     name         = "polystac"
//     function_zip = "polystac-lambda.zip"
//     backend      = "pgstac"
//     pg_dsn       = var.pg_dsn
//     vpc_subnet_ids         = aws_subnet.private[*].id
//     vpc_security_group_ids = [aws_security_group.lambda.id]
//   }
//
// or for OS:
//
//   module "polystac" {
//     source       = "github.com/example/polystac//deploy/terraform"
//     name         = "polystac"
//     function_zip = "polystac-lambda.zip"
//     backend      = "opensearch"
//     es_hosts     = aws_opensearch_domain.this.endpoint
//     es_username  = "admin"
//     es_password  = var.os_admin_password
//   }

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = { source = "hashicorp/aws", version = ">= 5.0" }
  }
}

variable "name"                   { type = string  default = "polystac" }
variable "function_zip"           { type = string }
variable "backend"                { type = string  default = "pgstac" }
variable "memory_mb"              { type = number  default = 512 }
variable "timeout_s"              { type = number  default = 30 }
variable "architecture"           { type = string  default = "arm64" }
variable "log_level"              { type = string  default = "info" }
variable "log_retention_days"     { type = number  default = 14 }
variable "landing_title"          { type = string  default = "PolyStac" }
variable "landing_description"    { type = string  default = "PolyStac STAC API" }

# pgstac
variable "pg_dsn"                 { type = string  default = "" sensitive = true }
variable "pg_pool_min"            { type = number  default = 0 }
variable "pg_pool_max"            { type = number  default = 2 }
variable "pg_use_api_hydrate"     { type = string  default = "false" }
variable "vpc_subnet_ids"         { type = list(string)  default = [] }
variable "vpc_security_group_ids" { type = list(string)  default = [] }

# opensearch / elasticsearch
variable "es_hosts"               { type = string  default = "" }
variable "es_username"            { type = string  default = "" sensitive = true }
variable "es_password"            { type = string  default = "" sensitive = true }
variable "es_verify_certs"        { type = string  default = "true" }
variable "es_refresh"             { type = string  default = "false" }
variable "es_index_prefix"        { type = string  default = "items_" }
variable "es_collections_index"   { type = string  default = "collections" }

# Optional: extra IAM policy ARNs to attach (e.g. one granting
# es:ESHttp* on your OpenSearch domain when using IAM-based auth).
variable "extra_policy_arns"      { type = list(string)  default = [] }

locals {
  is_pgstac    = var.backend == "pgstac"
  is_opensearch = contains(["opensearch", "elasticsearch"], var.backend)
  use_vpc       = local.is_pgstac && length(var.vpc_subnet_ids) > 0
}

resource "aws_iam_role" "exec" {
  name               = "${var.name}-exec"
  assume_role_policy = jsonencode({
    Version = "2012-10-17",
    Statement = [{
      Effect    = "Allow",
      Principal = { Service = "lambda.amazonaws.com" },
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "logs" {
  role       = aws_iam_role.exec.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy_attachment" "vpc" {
  count      = local.use_vpc ? 1 : 0
  role       = aws_iam_role.exec.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

resource "aws_iam_role_policy_attachment" "extras" {
  for_each   = toset(var.extra_policy_arns)
  role       = aws_iam_role.exec.name
  policy_arn = each.value
}

resource "aws_cloudwatch_log_group" "this" {
  name              = "/aws/lambda/${var.name}"
  retention_in_days = var.log_retention_days
}

resource "aws_lambda_function" "this" {
  function_name = var.name
  role          = aws_iam_role.exec.arn
  filename      = var.function_zip
  source_code_hash = filebase64sha256(var.function_zip)
  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = [var.architecture]
  memory_size   = var.memory_mb
  timeout       = var.timeout_s

  tracing_config { mode = "Active" }

  dynamic "vpc_config" {
    for_each = local.use_vpc ? [1] : []
    content {
      subnet_ids         = var.vpc_subnet_ids
      security_group_ids = var.vpc_security_group_ids
    }
  }

  environment {
    variables = {
      POLYSTAC_BACKEND          = var.backend
      POLYSTAC_LOG_FORMAT       = "json"
      POLYSTAC_LOG_LEVEL        = var.log_level
      POLYSTAC_TITLE            = var.landing_title
      POLYSTAC_DESCRIPTION      = var.landing_description
      POLYSTAC_LISTEN           = ":8080"

      POLYSTAC_PG_DSN             = var.pg_dsn
      POLYSTAC_PG_POOL_MIN        = tostring(var.pg_pool_min)
      POLYSTAC_PG_POOL_MAX        = tostring(var.pg_pool_max)
      POLYSTAC_PG_USE_API_HYDRATE = var.pg_use_api_hydrate

      POLYSTAC_ES_HOSTS              = var.es_hosts
      POLYSTAC_ES_USERNAME           = var.es_username
      POLYSTAC_ES_PASSWORD           = var.es_password
      POLYSTAC_ES_VERIFY_CERTS       = var.es_verify_certs
      POLYSTAC_ES_REFRESH            = var.es_refresh
      POLYSTAC_ES_INDEX_PREFIX       = var.es_index_prefix
      POLYSTAC_ES_COLLECTIONS_INDEX  = var.es_collections_index
    }
  }

  depends_on = [aws_cloudwatch_log_group.this]
}

resource "aws_lambda_function_url" "this" {
  function_name      = aws_lambda_function.this.function_name
  authorization_type = "NONE"

  cors {
    allow_origins  = ["*"]
    allow_headers  = ["*"]
    allow_methods  = ["GET", "POST", "PUT", "PATCH", "DELETE"]
    max_age        = 86400
  }
}

output "function_arn"  { value = aws_lambda_function.this.arn }
output "function_url"  { value = aws_lambda_function_url.this.function_url }
output "role_arn"      { value = aws_iam_role.exec.arn }
output "log_group"     { value = aws_cloudwatch_log_group.this.name }
