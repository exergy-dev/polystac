// Managed pgstac datastore: an RDS Postgres instance reachable from
// the supplied VPC, plus a Secrets Manager entry holding the master
// password, plus a security group.
//
// pgstac itself is a SQL-defined schema, not a Postgres extension —
// after this module brings the DB up, run `pypgstac migrate --dsn ...`
// once to install/upgrade the schema. PolyStac validates the version
// at startup and refuses to start on anything older than its
// MinSchemaVersion.
//
// To run the migration as part of `terraform apply`, set
// run_pypgstac_migrate = true. The default is false because the
// local-exec hook requires Python + pip on the runner host. For
// locked-down CI runners, run pypgstac yourself after apply or use a
// CodeBuild / one-shot ECS task variant (future work).

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws    = { source = "hashicorp/aws", version = ">= 5.0" }
    random = { source = "hashicorp/random", version = ">= 3.0" }
  }
}

resource "random_password" "master" {
  count   = var.master_password == "" ? 1 : 0
  length  = 32
  special = false
}

locals {
  password = var.master_password != "" ? var.master_password : random_password.master[0].result
  port     = 5432
  dsn = format(
    "postgresql://%s:%s@%s:%d/%s?sslmode=require",
    var.master_username,
    local.password,
    aws_db_instance.this.address,
    aws_db_instance.this.port,
    var.database_name,
  )
}

resource "aws_security_group" "this" {
  name        = "${var.name}-sg"
  description = "PolyStac pgstac DB inbound from configured client SGs"
  vpc_id      = var.vpc_id
  tags        = var.tags
}

resource "aws_security_group_rule" "ingress" {
  for_each                 = toset(var.client_security_group_ids)
  type                     = "ingress"
  from_port                = local.port
  to_port                  = local.port
  protocol                 = "tcp"
  source_security_group_id = each.value
  security_group_id        = aws_security_group.this.id
}

resource "aws_db_subnet_group" "this" {
  name       = "${var.name}-subnets"
  subnet_ids = var.subnet_ids
  tags       = var.tags
}

resource "aws_db_instance" "this" {
  identifier              = var.name
  engine                  = "postgres"
  engine_version          = var.engine_version
  instance_class          = var.instance_class
  allocated_storage       = var.allocated_storage_gb
  max_allocated_storage   = var.max_allocated_storage_gb
  storage_type            = "gp3"
  storage_encrypted       = true
  db_name                 = var.database_name
  username                = var.master_username
  password                = local.password
  port                    = local.port
  publicly_accessible     = false
  multi_az                = var.multi_az
  db_subnet_group_name    = aws_db_subnet_group.this.name
  vpc_security_group_ids  = [aws_security_group.this.id]
  parameter_group_name    = aws_db_parameter_group.this.name
  backup_retention_period = var.backup_retention_days
  deletion_protection     = var.deletion_protection
  skip_final_snapshot     = var.skip_final_snapshot
  apply_immediately       = var.apply_immediately
  tags                    = var.tags
}

resource "aws_db_parameter_group" "this" {
  name        = "${var.name}-params"
  family      = var.parameter_group_family
  description = "PolyStac pgstac parameter group"
  tags        = var.tags
}

resource "aws_secretsmanager_secret" "dsn" {
  name        = "${var.name}-dsn"
  description = "PolyStac pgstac DSN"
  tags        = var.tags
}

resource "aws_secretsmanager_secret_version" "dsn" {
  secret_id = aws_secretsmanager_secret.dsn.id
  secret_string = jsonencode({
    dsn      = local.dsn
    host     = aws_db_instance.this.address
    port     = aws_db_instance.this.port
    database = var.database_name
    username = var.master_username
    password = local.password
  })
}

# Optional: run `pypgstac migrate` against the new database. Off by
# default because local-exec requires Python + pip on the Terraform
# runner. If you can't run it locally, use a one-shot ECS task or run
# the migration command from a jumphost after apply (see
# docs/deploy-aws.md §pgstac).
resource "null_resource" "pypgstac_migrate" {
  count = var.run_pypgstac_migrate ? 1 : 0
  triggers = {
    db_id = aws_db_instance.this.id
  }
  provisioner "local-exec" {
    command     = "pypgstac migrate --dsn '${local.dsn}'"
    interpreter = ["bash", "-c"]
  }
  depends_on = [aws_db_instance.this]
}
