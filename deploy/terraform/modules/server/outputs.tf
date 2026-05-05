output "alb_dns_name" {
  value       = aws_lb.this.dns_name
  description = "Public DNS name of the ALB. Use as the STAC API base URL."
}

output "alb_arn" {
  value = aws_lb.this.arn
}

output "service_arn" {
  value = aws_ecs_service.this.id
}

output "task_role_arn" {
  value = aws_iam_role.task.arn
}

output "task_security_group_id" {
  value       = aws_security_group.task.id
  description = "Add to the datastore module's client_security_group_ids so the tasks can reach it."
}

output "log_group" {
  value = aws_cloudwatch_log_group.this.name
}

output "target_group_arn" {
  value = aws_lb_target_group.this.arn
}

output "cluster_arn" {
  value = local.cluster_arn
}
