# Minimal Terraform module wrapping a Lambda function URL for PolyStac.
# Operators consume it as a child module:
#
#   module "polystac" {
#     source       = "github.com/example/polystac//deploy/terraform"
#     function_zip = "polystac-lambda.zip"   # contains `bootstrap` binary
#     backend      = "pgstac"
#     pg_dsn       = var.pg_dsn
#   }
#
# Build the zip with:
#   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
#     -o bootstrap ./cmd/polystac-lambda
#   zip polystac-lambda.zip bootstrap

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = { source = "hashicorp/aws", version = ">= 5.0" }
  }
}

variable "name"          { type = string  default = "polystac" }
variable "function_zip"  { type = string }
variable "backend"       { type = string  default = "pgstac" }
variable "pg_dsn"        { type = string  default = "" sensitive = true }
variable "es_hosts"      { type = string  default = "" }
variable "es_username"   { type = string  default = "" sensitive = true }
variable "es_password"   { type = string  default = "" sensitive = true }
variable "memory_mb"     { type = number  default = 512 }
variable "timeout_s"     { type = number  default = 30 }

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

resource "aws_lambda_function" "this" {
  function_name = var.name
  role          = aws_iam_role.exec.arn
  filename      = var.function_zip
  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["x86_64"]
  memory_size   = var.memory_mb
  timeout       = var.timeout_s

  environment {
    variables = {
      POLYSTAC_BACKEND     = var.backend
      POLYSTAC_PG_DSN      = var.pg_dsn
      POLYSTAC_ES_HOSTS    = var.es_hosts
      POLYSTAC_ES_USERNAME = var.es_username
      POLYSTAC_ES_PASSWORD = var.es_password
      POLYSTAC_LOG_FORMAT  = "json"
    }
  }
}

resource "aws_lambda_function_url" "this" {
  function_name      = aws_lambda_function.this.function_name
  authorization_type = "NONE"
}

output "function_url" {
  value = aws_lambda_function_url.this.function_url
}
