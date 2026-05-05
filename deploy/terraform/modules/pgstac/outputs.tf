output "dsn" {
  value       = local.dsn
  sensitive   = true
  description = "Full Postgres DSN with credentials. Pass to a polystac runtime module's pg_dsn input."
}

output "endpoint" {
  value = aws_db_instance.this.address
}

output "port" {
  value = aws_db_instance.this.port
}

output "database_name" {
  value = var.database_name
}

output "security_group_id" {
  value       = aws_security_group.this.id
  description = "DB-side security group. Add the runtime's SG to var.client_security_group_ids."
}

output "secret_arn" {
  value       = aws_secretsmanager_secret.dsn.arn
  description = "Secrets Manager ARN holding the DSN + parts."
}

output "instance_arn" {
  value = aws_db_instance.this.arn
}
