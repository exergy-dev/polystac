// Example: PolyStac on ECS Fargate behind an ALB + managed OpenSearch.
//
// Provisions:
//   - AWS OpenSearch Service domain (modules/opensearch).
//   - ECS service (modules/server) running the polystac Docker image.
//
// Push the container image first:
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

variable "task_subnet_ids" {
  type = list(string)
}

variable "image" {
  type = string
}

variable "listener_certificate_arn" {
  type    = string
  default = ""
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
  source = "../../modules/server"
  name   = "polystac"
  image  = var.image

  vpc_id            = var.vpc_id
  public_subnet_ids = var.public_subnet_ids
  task_subnet_ids   = var.task_subnet_ids

  backend     = "opensearch"
  es_hosts    = module.opensearch.endpoint
  es_username = module.opensearch.admin_username
  es_password = module.opensearch.admin_password

  listener_certificate_arn = var.listener_certificate_arn
}

output "alb_dns_name" {
  value = module.polystac.alb_dns_name
}

output "opensearch_endpoint" {
  value = module.opensearch.endpoint
}

output "opensearch_secret_arn" {
  value = module.opensearch.secret_arn
}
