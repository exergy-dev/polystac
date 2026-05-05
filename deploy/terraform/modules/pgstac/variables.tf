variable "name" {
  type        = string
  default     = "polystac-pgstac"
  description = "Name prefix for the DB, SG, secret, parameter group."
}

variable "vpc_id" {
  type        = string
  description = "VPC the DB lives in."
}

variable "subnet_ids" {
  type        = list(string)
  description = "Subnet IDs (≥ 2 across AZs) for the DB subnet group."
}

variable "client_security_group_ids" {
  type        = list(string)
  default     = []
  description = "Security groups allowed to connect to the DB on 5432."
}

variable "engine_version" {
  type        = string
  default     = "16.4"
  description = "Postgres engine version. pgstac supports 13+."
}

variable "parameter_group_family" {
  type        = string
  default     = "postgres16"
  description = "Must match the engine_version major. e.g. postgres16 for 16.x."
}

variable "instance_class" {
  type    = string
  default = "db.t4g.medium"
}

variable "allocated_storage_gb" {
  type    = number
  default = 20
}

variable "max_allocated_storage_gb" {
  type    = number
  default = 100
}

variable "multi_az" {
  type    = bool
  default = false
}

variable "database_name" {
  type    = string
  default = "stac"
}

variable "master_username" {
  type    = string
  default = "stac"
}

variable "master_password" {
  type        = string
  default     = ""
  sensitive   = true
  description = "If empty, a random password is generated and stored in Secrets Manager."
}

variable "backup_retention_days" {
  type    = number
  default = 7
}

variable "deletion_protection" {
  type    = bool
  default = false
}

variable "skip_final_snapshot" {
  type    = bool
  default = true
}

variable "apply_immediately" {
  type    = bool
  default = false
}

variable "run_pypgstac_migrate" {
  type        = bool
  default     = false
  description = "Run `pypgstac migrate` via local-exec after the DB comes up. Requires Python + pip on the Terraform runner."
}

variable "tags" {
  type    = map(string)
  default = {}
}
