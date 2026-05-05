// Identical surface to the pre-split module.
output "function_arn" { value = module.lambda.function_arn }
output "function_url" { value = module.lambda.function_url }
output "role_arn" { value = module.lambda.role_arn }
output "log_group" { value = module.lambda.log_group }
