output "endpoint" {
  value       = "https://${aws_opensearch_domain.this.endpoint}"
  description = "Full HTTPS endpoint to pass to a polystac runtime module's es_hosts input."
}

output "domain_arn" {
  value = aws_opensearch_domain.this.arn
}

output "admin_username" {
  value = local.use_internal_user ? var.admin_username : ""
}

output "admin_password" {
  value     = local.admin_password
  sensitive = true
}

output "secret_arn" {
  value       = local.use_internal_user ? aws_secretsmanager_secret.creds[0].arn : ""
  description = "Secrets Manager ARN holding the admin creds. Empty when access_model = iam_restricted."
}
