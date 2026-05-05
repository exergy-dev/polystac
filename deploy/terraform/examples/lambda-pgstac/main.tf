// Example: PolyStac Lambda + managed pgstac (RDS Postgres).
//
// Provisions:
//   - RDS Postgres (modules/pgstac) with a generated admin password
//     in Secrets Manager.
//   - Lambda function (modules/lambda), VPC-attached so it can reach
//     the DB.
//   - The Lambda's SG is added to the DB's allowed clients.
//
// Run `pypgstac migrate --dsn $(terraform output -raw pgstac_dsn)`
// once after `terraform apply` to install the pgstac schema (or set
// var.run_pypgstac_migrate = true).

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

variable "vpc_id" {
  type = string
}

variable "private_subnet_ids" {
  type        = list(string)
  description = "Private subnets in ≥ 2 AZs that can reach the DB."
}

variable "run_pypgstac_migrate" {
  type    = bool
  default = false
}

# Lambda needs its own SG so we can grant it inbound access to the DB.
resource "aws_security_group" "lambda" {
  name        = "polystac-lambda"
  description = "PolyStac Lambda ENI"
  vpc_id      = var.vpc_id
}

resource "aws_security_group_rule" "lambda_egress" {
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.lambda.id
}

module "pgstac" {
  source = "../../modules/pgstac"
  name   = "polystac-pgstac"

  vpc_id                    = var.vpc_id
  subnet_ids                = var.private_subnet_ids
  client_security_group_ids = [aws_security_group.lambda.id]

  run_pypgstac_migrate = var.run_pypgstac_migrate
}

module "polystac" {
  source       = "../../modules/lambda"
  name         = "polystac"
  function_zip = var.function_zip
  backend      = "pgstac"

  pg_dsn                 = module.pgstac.dsn
  vpc_subnet_ids         = var.private_subnet_ids
  vpc_security_group_ids = [aws_security_group.lambda.id]
}

output "function_url" {
  value = module.polystac.function_url
}

output "pgstac_dsn" {
  value     = module.pgstac.dsn
  sensitive = true
}

output "pgstac_secret_arn" {
  value = module.pgstac.secret_arn
}
