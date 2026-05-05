// Long-running PolyStac on ECS Fargate behind an Application Load
// Balancer. Backend-agnostic: takes a backend name plus the env vars
// the polystac binary reads, runs the same Docker image the
// production Dockerfile builds (port 8000, /_health probe).
//
// Wire this with modules/pgstac or modules/opensearch (or supply
// pg_dsn / es_hosts directly) — see the deploy/terraform/examples/
// stacks.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = { source = "hashicorp/aws", version = ">= 5.0" }
  }
}

locals {
  is_pgstac      = var.backend == "pgstac"
  is_opensearch  = contains(["opensearch", "elasticsearch"], var.backend)
  container_port = 8000
}

# ---- ECS cluster (created if not supplied) -------------------------

resource "aws_ecs_cluster" "this" {
  count = var.cluster_arn == "" ? 1 : 0
  name  = "${var.name}-cluster"
  tags  = var.tags
}

locals {
  cluster_arn = var.cluster_arn != "" ? var.cluster_arn : aws_ecs_cluster.this[0].arn
}

# ---- Security groups ----------------------------------------------

resource "aws_security_group" "alb" {
  name        = "${var.name}-alb"
  description = "PolyStac ALB inbound from internet"
  vpc_id      = var.vpc_id
  tags        = var.tags
}

resource "aws_security_group_rule" "alb_ingress_http" {
  count             = var.listener_certificate_arn == "" ? 1 : 0
  type              = "ingress"
  from_port         = 80
  to_port           = 80
  protocol          = "tcp"
  cidr_blocks       = var.alb_ingress_cidrs
  security_group_id = aws_security_group.alb.id
}

resource "aws_security_group_rule" "alb_ingress_https" {
  count             = var.listener_certificate_arn != "" ? 1 : 0
  type              = "ingress"
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = var.alb_ingress_cidrs
  security_group_id = aws_security_group.alb.id
}

resource "aws_security_group_rule" "alb_egress" {
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.alb.id
}

resource "aws_security_group" "task" {
  name        = "${var.name}-task"
  description = "PolyStac task ENI; inbound from ALB only"
  vpc_id      = var.vpc_id
  tags        = var.tags
}

resource "aws_security_group_rule" "task_ingress" {
  type                     = "ingress"
  from_port                = local.container_port
  to_port                  = local.container_port
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.alb.id
  security_group_id        = aws_security_group.task.id
}

resource "aws_security_group_rule" "task_egress" {
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.task.id
}

# ---- ALB ----------------------------------------------------------

resource "aws_lb" "this" {
  name               = substr(var.name, 0, 32)
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.public_subnet_ids
  tags               = var.tags
}

resource "aws_lb_target_group" "this" {
  name        = substr("${var.name}-tg", 0, 32)
  port        = local.container_port
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = var.vpc_id

  health_check {
    path                = "/_health"
    matcher             = "200"
    interval            = 15
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }

  deregistration_delay = 30
  tags                 = var.tags
}

resource "aws_lb_listener" "http" {
  count             = var.listener_certificate_arn == "" ? 1 : 0
  load_balancer_arn = aws_lb.this.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this.arn
  }
}

resource "aws_lb_listener" "https" {
  count             = var.listener_certificate_arn != "" ? 1 : 0
  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.listener_certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this.arn
  }
}

# ---- IAM ----------------------------------------------------------

resource "aws_iam_role" "execution" {
  name = "${var.name}-exec"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "execution_managed" {
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_role" "task" {
  name = "${var.name}-task"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "task_extras" {
  for_each   = toset(var.extra_policy_arns)
  role       = aws_iam_role.task.name
  policy_arn = each.value
}

# ---- Logs ---------------------------------------------------------

resource "aws_cloudwatch_log_group" "this" {
  name              = "/ecs/${var.name}"
  retention_in_days = var.log_retention_days
  tags              = var.tags
}

# ---- Task definition + service ------------------------------------

locals {
  env_vars = [
    { name = "POLYSTAC_BACKEND", value = var.backend },
    { name = "POLYSTAC_LOG_FORMAT", value = "json" },
    { name = "POLYSTAC_LOG_LEVEL", value = var.log_level },
    { name = "POLYSTAC_TITLE", value = var.landing_title },
    { name = "POLYSTAC_DESCRIPTION", value = var.landing_description },
    { name = "POLYSTAC_LISTEN", value = ":${local.container_port}" },

    { name = "POLYSTAC_PG_POOL_MIN", value = tostring(var.pg_pool_min) },
    { name = "POLYSTAC_PG_POOL_MAX", value = tostring(var.pg_pool_max) },
    { name = "POLYSTAC_PG_USE_API_HYDRATE", value = var.pg_use_api_hydrate },

    { name = "POLYSTAC_ES_VERIFY_CERTS", value = var.es_verify_certs },
    { name = "POLYSTAC_ES_REFRESH", value = var.es_refresh },
    { name = "POLYSTAC_ES_INDEX_PREFIX", value = var.es_index_prefix },
    { name = "POLYSTAC_ES_COLLECTIONS_INDEX", value = var.es_collections_index },
  ]

  # Secret-bearing fields use ECS `secrets` so they're pulled from
  # Secrets Manager / SSM at task start without ever appearing in
  # the task def or task role policy.
  secrets = concat(
    var.pg_dsn_secret_arn != "" ? [{ name = "POLYSTAC_PG_DSN", valueFrom = var.pg_dsn_secret_arn }] : [],
    var.pg_dsn != "" ? [{ name = "POLYSTAC_PG_DSN_LITERAL", valueFrom = var.pg_dsn }] : [],
    var.es_hosts != "" ? [] : [],
  )

  # Plain-text env when caller didn't supply a secret arn.
  plain_secrets_env = concat(
    var.pg_dsn_secret_arn == "" && var.pg_dsn != "" ? [{ name = "POLYSTAC_PG_DSN", value = var.pg_dsn }] : [],
    var.es_hosts != "" ? [{ name = "POLYSTAC_ES_HOSTS", value = var.es_hosts }] : [],
    var.es_username != "" ? [{ name = "POLYSTAC_ES_USERNAME", value = var.es_username }] : [],
    var.es_password != "" ? [{ name = "POLYSTAC_ES_PASSWORD", value = var.es_password }] : [],
  )
}

resource "aws_ecs_task_definition" "this" {
  family                   = var.name
  cpu                      = var.cpu
  memory                   = var.memory
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn
  runtime_platform {
    cpu_architecture        = upper(var.architecture)
    operating_system_family = "LINUX"
  }

  container_definitions = jsonencode([{
    name      = "polystac"
    image     = var.image
    essential = true
    portMappings = [{
      containerPort = local.container_port
      protocol      = "tcp"
    }]
    environment = concat(local.env_vars, local.plain_secrets_env)
    secrets     = [for s in local.secrets : s if s.name != "POLYSTAC_PG_DSN_LITERAL"]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.this.name
        awslogs-stream-prefix = "polystac"
        awslogs-region        = data.aws_region.current.region
      }
    }
    healthCheck = {
      command     = ["CMD-SHELL", "wget -qO- http://127.0.0.1:${local.container_port}/_health || exit 1"]
      interval    = 15
      timeout     = 5
      retries     = 3
      startPeriod = 10
    }
  }])

  tags = var.tags
}

data "aws_region" "current" {}

resource "aws_ecs_service" "this" {
  name            = var.name
  cluster         = local.cluster_arn
  task_definition = aws_ecs_task_definition.this.arn
  desired_count   = var.desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.task_subnet_ids
    security_groups  = [aws_security_group.task.id]
    assign_public_ip = var.assign_public_ip
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.this.arn
    container_name   = "polystac"
    container_port   = local.container_port
  }

  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200

  depends_on = [
    aws_lb_listener.http,
    aws_lb_listener.https,
  ]
  tags = var.tags
}
