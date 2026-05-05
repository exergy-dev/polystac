// Managed AWS OpenSearch Service domain configured for STAC
// workloads. Two access models:
//
//   public_with_internal_user (default):
//     Public endpoint, FGAC enabled, an admin internal user is
//     created with a generated password stored in Secrets Manager.
//     The runtime module wires POLYSTAC_ES_USERNAME / _PASSWORD.
//
//   iam_restricted:
//     Public endpoint, IAM-only access policy. Caller supplies a
//     list of IAM principal ARNs that may issue es:ESHttp*. PolyStac
//     does not yet sign requests with SigV4 (roadmap), so this mode
//     is for environments where you front the domain with an IAM-
//     authorizing proxy.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws    = { source = "hashicorp/aws", version = ">= 5.0" }
    random = { source = "hashicorp/random", version = ">= 3.0" }
  }
}

resource "random_password" "admin" {
  count            = var.access_model == "public_with_internal_user" ? 1 : 0
  length           = 24
  override_special = "!@#$%^&*()-_=+"
}

locals {
  use_internal_user = var.access_model == "public_with_internal_user"
  admin_password    = local.use_internal_user ? random_password.admin[0].result : ""
}

resource "aws_opensearch_domain" "this" {
  domain_name    = var.name
  engine_version = var.engine_version
  tags           = var.tags

  cluster_config {
    instance_type            = var.instance_type
    instance_count           = var.instance_count
    zone_awareness_enabled   = var.instance_count > 1
    dedicated_master_enabled = false
  }

  ebs_options {
    ebs_enabled = true
    volume_type = "gp3"
    volume_size = var.volume_size_gb
  }

  encrypt_at_rest {
    enabled = true
  }

  node_to_node_encryption {
    enabled = true
  }

  domain_endpoint_options {
    enforce_https       = true
    tls_security_policy = "Policy-Min-TLS-1-2-2019-07"
  }

  advanced_security_options {
    enabled                        = local.use_internal_user
    internal_user_database_enabled = local.use_internal_user
    anonymous_auth_enabled         = false

    dynamic "master_user_options" {
      for_each = local.use_internal_user ? [1] : []
      content {
        master_user_name     = var.admin_username
        master_user_password = local.admin_password
      }
    }
  }

  access_policies = local.use_internal_user ? jsonencode({
    Version = "2012-10-17",
    Statement = [{
      Effect    = "Allow",
      Principal = { AWS = "*" },
      Action    = "es:*",
      Resource  = "arn:aws:es:*:*:domain/${var.name}/*"
    }]
    }) : jsonencode({
    Version = "2012-10-17",
    Statement = [{
      Effect    = "Allow",
      Principal = { AWS = var.iam_principal_arns },
      Action    = "es:ESHttp*",
      Resource  = "arn:aws:es:*:*:domain/${var.name}/*"
    }]
  })
}

resource "aws_secretsmanager_secret" "creds" {
  count       = local.use_internal_user ? 1 : 0
  name        = "${var.name}-os-admin"
  description = "PolyStac OpenSearch admin internal-user creds"
  tags        = var.tags
}

resource "aws_secretsmanager_secret_version" "creds" {
  count     = local.use_internal_user ? 1 : 0
  secret_id = aws_secretsmanager_secret.creds[0].id
  secret_string = jsonencode({
    username = var.admin_username
    password = local.admin_password
    endpoint = "https://${aws_opensearch_domain.this.endpoint}"
  })
}
