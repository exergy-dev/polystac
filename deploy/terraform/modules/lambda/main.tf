// PolyStac on AWS Lambda. Backend-agnostic: takes a backend name plus
// the env vars its binary reads (POLYSTAC_*), provisions a Lambda
// function with a public Function URL, IAM role, and CloudWatch log
// group. For pgstac, supply vpc_subnet_ids + vpc_security_group_ids
// to attach the function's ENI to a VPC that can reach the database.
//
// Build the deployment zip first:
//
//   GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
//     go build -trimpath -ldflags='-s -w' -o bootstrap ./cmd/polystac-lambda
//   zip polystac-lambda.zip bootstrap
//
// Compose into a stack via the matching example in
// deploy/terraform/examples/.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = { source = "hashicorp/aws", version = ">= 5.0" }
  }
}

locals {
  is_pgstac     = var.backend == "pgstac"
  is_opensearch = contains(["opensearch", "elasticsearch"], var.backend)
  use_vpc       = local.is_pgstac && length(var.vpc_subnet_ids) > 0
}

resource "aws_iam_role" "exec" {
  name = "${var.name}-exec"
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
  function_name    = var.name
  role             = aws_iam_role.exec.arn
  filename         = var.function_zip
  source_code_hash = filebase64sha256(var.function_zip)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = [var.architecture]
  memory_size      = var.memory_mb
  timeout          = var.timeout_s

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
      POLYSTAC_BACKEND     = var.backend
      POLYSTAC_LOG_FORMAT  = "json"
      POLYSTAC_LOG_LEVEL   = var.log_level
      POLYSTAC_TITLE       = var.landing_title
      POLYSTAC_DESCRIPTION = var.landing_description
      POLYSTAC_LISTEN      = ":8080"

      POLYSTAC_PG_DSN             = var.pg_dsn
      POLYSTAC_PG_POOL_MIN        = tostring(var.pg_pool_min)
      POLYSTAC_PG_POOL_MAX        = tostring(var.pg_pool_max)
      POLYSTAC_PG_USE_API_HYDRATE = var.pg_use_api_hydrate

      POLYSTAC_ES_HOSTS             = var.es_hosts
      POLYSTAC_ES_USERNAME          = var.es_username
      POLYSTAC_ES_PASSWORD          = var.es_password
      POLYSTAC_ES_VERIFY_CERTS      = var.es_verify_certs
      POLYSTAC_ES_REFRESH           = var.es_refresh
      POLYSTAC_ES_INDEX_PREFIX      = var.es_index_prefix
      POLYSTAC_ES_COLLECTIONS_INDEX = var.es_collections_index
    }
  }

  depends_on = [aws_cloudwatch_log_group.this]
}

resource "aws_lambda_function_url" "this" {
  function_name      = aws_lambda_function.this.function_name
  authorization_type = "NONE"

  cors {
    allow_origins = ["*"]
    allow_headers = ["*"]
    allow_methods = ["GET", "POST", "PUT", "PATCH", "DELETE"]
    max_age       = 86400
  }
}
