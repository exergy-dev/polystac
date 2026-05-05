// Legacy variable surface kept identical to the pre-split module so
// existing callers don't have to update anything. New compositions
// should use the variables on the relevant submodule directly.

variable "name" {
  type    = string
  default = "polystac"
}

variable "function_zip" {
  type = string
}

variable "backend" {
  type    = string
  default = "pgstac"
}

variable "memory_mb" {
  type    = number
  default = 512
}

variable "timeout_s" {
  type    = number
  default = 30
}

variable "architecture" {
  type    = string
  default = "arm64"
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

variable "pg_dsn" {
  type      = string
  default   = ""
  sensitive = true
}

variable "pg_pool_min" {
  type    = number
  default = 0
}

variable "pg_pool_max" {
  type    = number
  default = 2
}

variable "pg_use_api_hydrate" {
  type    = string
  default = "false"
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

variable "es_verify_certs" {
  type    = string
  default = "true"
}

variable "es_refresh" {
  type    = string
  default = "false"
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
