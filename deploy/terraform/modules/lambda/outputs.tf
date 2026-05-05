output "function_arn" { value = aws_lambda_function.this.arn }
output "function_name" { value = aws_lambda_function.this.function_name }
output "function_url" { value = aws_lambda_function_url.this.function_url }
output "role_arn" { value = aws_iam_role.exec.arn }
output "role_name" { value = aws_iam_role.exec.name }
output "log_group" { value = aws_cloudwatch_log_group.this.name }
