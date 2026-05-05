variable "name" {
  type        = string
  default     = "polystac-os"
  description = "Domain name. Must be lowercase, 3-28 chars, [a-z0-9-]."
}

variable "engine_version" {
  type    = string
  default = "OpenSearch_2.13"
}

variable "instance_type" {
  type    = string
  default = "t3.small.search"
}

variable "instance_count" {
  type    = number
  default = 1
}

variable "volume_size_gb" {
  type    = number
  default = 20
}

variable "access_model" {
  type        = string
  default     = "public_with_internal_user"
  description = "One of public_with_internal_user | iam_restricted."
  validation {
    condition     = contains(["public_with_internal_user", "iam_restricted"], var.access_model)
    error_message = "access_model must be public_with_internal_user or iam_restricted."
  }
}

variable "admin_username" {
  type    = string
  default = "polystac"
}

variable "iam_principal_arns" {
  type        = list(string)
  default     = []
  description = "Used only when access_model = iam_restricted. ARNs allowed to call es:ESHttp*."
}

variable "tags" {
  type    = map(string)
  default = {}
}
