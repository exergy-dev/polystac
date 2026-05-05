variable "name" {
  type    = string
  default = "polystac"
}

variable "image" {
  type        = string
  description = "Container image to run (e.g. ghcr.io/example/polystac:0.1.0). Same Dockerfile as the production image; port 8000."
}

variable "vpc_id" {
  type = string
}

variable "public_subnet_ids" {
  type        = list(string)
  description = "Subnets the ALB lives in (≥ 2 across AZs)."
}

variable "task_subnet_ids" {
  type        = list(string)
  description = "Subnets the Fargate tasks live in. Use private subnets for pgstac (with a NAT for outbound) and public/private as appropriate for OS."
}

variable "cluster_arn" {
  type        = string
  default     = ""
  description = "Reuse an existing ECS cluster. Empty = create one."
}

variable "backend" {
  type    = string
  default = "pgstac"
}

variable "cpu" {
  type    = number
  default = 512
}

variable "memory" {
  type    = number
  default = 1024
}

variable "architecture" {
  type    = string
  default = "ARM64"
  validation {
    condition     = contains(["X86_64", "ARM64"], upper(var.architecture))
    error_message = "architecture must be X86_64 or ARM64."
  }
}

variable "desired_count" {
  type    = number
  default = 2
}

variable "assign_public_ip" {
  type        = bool
  default     = false
  description = "Set true when running tasks in public subnets without a NAT."
}

variable "alb_ingress_cidrs" {
  type    = list(string)
  default = ["0.0.0.0/0"]
}

variable "listener_certificate_arn" {
  type        = string
  default     = ""
  description = "ACM certificate ARN. When set, the ALB listens on 443 with TLS; otherwise port 80."
}

variable "log_level" {
  type    = string
  default = "info"
}

variable "log_retention_days" {
  type    = number
  default = 14
}

variable "landing_title" {
  type    = string
  default = "PolyStac"
}

variable "landing_description" {
  type    = string
  default = "PolyStac STAC API"
}

# pgstac
variable "pg_dsn" {
  type        = string
  default     = ""
  sensitive   = true
  description = "Plain-text DSN. Prefer pg_dsn_secret_arn for production."
}

variable "pg_dsn_secret_arn" {
  type        = string
  default     = ""
  description = "Secrets Manager ARN holding the DSN under a `dsn` JSON key. The task definition fetches it at start time."
}

variable "pg_pool_min" {
  type    = number
  default = 2
}

variable "pg_pool_max" {
  type    = number
  default = 20
}

variable "pg_use_api_hydrate" {
  type    = string
  default = "false"
}

# opensearch / elasticsearch
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

variable "es_verify_certs" {
  type    = string
  default = "true"
}

variable "es_refresh" {
  type    = string
  default = "wait_for"
}

variable "es_index_prefix" {
  type    = string
  default = "items_"
}

variable "es_collections_index" {
  type    = string
  default = "collections"
}

variable "extra_policy_arns" {
  type    = list(string)
  default = []
}

variable "tags" {
  type    = map(string)
  default = {}
}
