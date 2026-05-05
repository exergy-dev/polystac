// Example: PolyStac on ECS Fargate behind an ALB + managed pgstac.
//
// Provisions:
//   - RDS Postgres (modules/pgstac).
//   - ECS service (modules/server) running the polystac Docker image.
//   - The task SG is added to the DB's allowed clients automatically.
//
// Push the polystac container image first:
//   docker build -t $REPO/polystac:0.1.0 .
//   docker push $REPO/polystac:0.1.0

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

variable "vpc_id" {
  type = string
}

variable "public_subnet_ids" {
  type = list(string)
}

variable "private_subnet_ids" {
  type = list(string)
}

variable "image" {
  type        = string
  description = "Docker image to run, e.g. ghcr.io/example/polystac:0.1.0."
}

variable "listener_certificate_arn" {
  type    = string
  default = ""
}

variable "run_pypgstac_migrate" {
  type    = bool
  default = false
}

module "pgstac" {
  source = "../../modules/pgstac"
  name   = "polystac-pgstac"

  vpc_id                    = var.vpc_id
  subnet_ids                = var.private_subnet_ids
  client_security_group_ids = [module.polystac.task_security_group_id]

  run_pypgstac_migrate = var.run_pypgstac_migrate
}

module "polystac" {
  source = "../../modules/server"
  name   = "polystac"
  image  = var.image

  vpc_id            = var.vpc_id
  public_subnet_ids = var.public_subnet_ids
  task_subnet_ids   = var.private_subnet_ids

  backend           = "pgstac"
  pg_dsn_secret_arn = module.pgstac.secret_arn

  listener_certificate_arn = var.listener_certificate_arn
}

output "alb_dns_name" {
  value = module.polystac.alb_dns_name
}

output "pgstac_secret_arn" {
  value = module.pgstac.secret_arn
}
