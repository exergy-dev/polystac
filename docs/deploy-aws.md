# Deploying PolyStac on AWS

PolyStac runs in two flavors and supports two managed datastores. The Terraform layout under [`deploy/terraform/`](../deploy/terraform/) ships **composable modules** so you mix and match:

| Runtime | Datastore | Example stack |
|---|---|---|
| Lambda | bring-your-own | [`examples/lambda-byo/`](../deploy/terraform/examples/lambda-byo/) |
| Lambda | managed pgstac (RDS Postgres) | [`examples/lambda-pgstac/`](../deploy/terraform/examples/lambda-pgstac/) |
| Lambda | managed OpenSearch | [`examples/lambda-opensearch/`](../deploy/terraform/examples/lambda-opensearch/) |
| ECS Fargate (long-running) | managed pgstac | [`examples/server-pgstac/`](../deploy/terraform/examples/server-pgstac/) |
| ECS Fargate | managed OpenSearch | [`examples/server-opensearch/`](../deploy/terraform/examples/server-opensearch/) |

Each example wires a runtime module (`modules/lambda` or `modules/server`) to a datastore module (`modules/pgstac` or `modules/opensearch`). Use any module standalone too — point an existing Lambda at an existing pgstac with `module "polystac" { source = "github.com/example/polystac//deploy/terraform/modules/lambda" ... }`.

Cold start measured locally: **~200–350 ms** for both runtimes, both backends — well inside SDD §NF-2.

## Module catalog

### `modules/lambda`

PolyStac Lambda function with a public Function URL, IAM role, log group. VPC-attached when pgstac is selected and `vpc_subnet_ids` is supplied. Inputs: `name`, `function_zip`, `backend`, full `POLYSTAC_*` env passthrough. Outputs: `function_arn`, `function_url`, `role_arn`, `log_group`.

### `modules/server`

Long-running PolyStac on ECS Fargate behind an Application Load Balancer. Same env-var contract as `modules/lambda`. Pulls a Docker image you've published (uses the production `Dockerfile`; port 8000; health probe at `/_health`). Outputs: `alb_dns_name`, `service_arn`, `task_role_arn`, `task_security_group_id`, `target_group_arn`.

### `modules/pgstac`

RDS Postgres with EBS encryption, a Secrets Manager entry holding the DSN, a security group inbound on 5432 from configurable client SGs. Optional `run_pypgstac_migrate = true` triggers a `local-exec` running `pypgstac migrate` after the DB comes up (requires Python on the runner). Outputs: `dsn` (sensitive), `endpoint`, `port`, `security_group_id`, `secret_arn`.

### `modules/opensearch`

AWS OpenSearch Service domain with TLS, encryption-at-rest, FGAC. Two access models: `public_with_internal_user` (default — a generated admin password lives in Secrets Manager) and `iam_restricted` (you supply ARNs). Outputs: `endpoint`, `admin_username`, `admin_password` (sensitive), `domain_arn`, `secret_arn`.

## 1. Build the deployment package

`provided.al2023` runs the Go binary directly; the binary must be named `bootstrap` and live at the root of the package.

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -ldflags='-s -w' -o bootstrap ./cmd/polystac-lambda
zip polystac-lambda.zip bootstrap
```

For ECS, build and push the production image:

```sh
docker build -t $REPO/polystac:0.1.0 .
docker push $REPO/polystac:0.1.0
```

`arm64` (Graviton) is ~20% cheaper per Lambda invocation and saves on Fargate too. Use `x86_64` only if a colocated dependency requires it.

## 2. Deploy: Lambda + managed pgstac

```hcl
module "polystac" {
  source       = "github.com/example/polystac//deploy/terraform/examples/lambda-pgstac"
  function_zip = "polystac-lambda.zip"

  vpc_id             = aws_vpc.main.id
  private_subnet_ids = aws_subnet.private[*].id

  # Run pypgstac on the Terraform runner. Skip and run it manually
  # after apply if your runner can't pip-install pypgstac.
  run_pypgstac_migrate = true
}

output "function_url" { value = module.polystac.function_url }
```

If `run_pypgstac_migrate = false` (default), run the migration once after `terraform apply`:

```sh
DSN=$(terraform output -raw pgstac_dsn)
pypgstac migrate --dsn "$DSN"
```

## 3. Deploy: Lambda + managed OpenSearch

```hcl
module "polystac" {
  source       = "github.com/example/polystac//deploy/terraform/examples/lambda-opensearch"
  function_zip = "polystac-lambda.zip"
  domain_name  = "polystac"
}

output "function_url"        { value = module.polystac.function_url }
output "opensearch_endpoint" { value = module.polystac.opensearch_endpoint }
```

OpenSearch fine-grained access control is enabled by default with a generated admin password in Secrets Manager. The Lambda reads it via env vars set at apply time. For IAM-signed access (no shared secrets), use `modules/opensearch` directly with `access_model = "iam_restricted"` and a SigV4-signing reverse proxy in front — full IAM signing inside PolyStac is on the v1.1 roadmap.

## 4. Deploy: ECS server + managed pgstac

For sustained traffic ECS Fargate is cheaper per-request than Lambda once you hit ~10 RPS sustained. Same composition pattern:

```hcl
module "polystac" {
  source = "github.com/example/polystac//deploy/terraform/examples/server-pgstac"

  vpc_id             = aws_vpc.main.id
  public_subnet_ids  = aws_subnet.public[*].id
  private_subnet_ids = aws_subnet.private[*].id
  image              = "ghcr.io/example/polystac:0.1.0"

  # Optional ACM cert; if omitted the ALB listens on port 80 plain HTTP.
  listener_certificate_arn = aws_acm_certificate.api.arn
}

output "alb_dns_name" { value = module.polystac.alb_dns_name }
```

## 5. Deploy: ECS server + managed OpenSearch

```hcl
module "polystac" {
  source = "github.com/example/polystac//deploy/terraform/examples/server-opensearch"

  vpc_id            = aws_vpc.main.id
  public_subnet_ids = aws_subnet.public[*].id
  task_subnet_ids   = aws_subnet.private[*].id
  image             = "ghcr.io/example/polystac:0.1.0"
  domain_name       = "polystac"
}

output "alb_dns_name"        { value = module.polystac.alb_dns_name }
output "opensearch_endpoint" { value = module.polystac.opensearch_endpoint }
```

## 6. Bring-your-own datastore (Lambda)

Already running pgstac or OpenSearch? Skip the datastore module:

```hcl
module "polystac" {
  source       = "github.com/example/polystac//deploy/terraform/examples/lambda-byo"
  function_zip = "polystac-lambda.zip"
  backend      = "pgstac"
  pg_dsn       = var.existing_pg_dsn

  vpc_subnet_ids         = aws_subnet.private[*].id
  vpc_security_group_ids = [aws_security_group.lambda.id]
}
```

The legacy single-module entry remains source-compatible for callers from before the layout split:

```hcl
module "polystac" {
  source       = "github.com/example/polystac//deploy/terraform"
  function_zip = "polystac-lambda.zip"
  backend      = "pgstac"
  pg_dsn       = var.existing_pg_dsn
  vpc_subnet_ids         = aws_subnet.private[*].id
  vpc_security_group_ids = [aws_security_group.lambda.id]
}
```

The legacy module is now a passthrough to `modules/lambda`; same variables, same outputs.

## 7. Operational notes

### Connection pooling (pgstac on Lambda)

Each Lambda instance is single-shot; concurrency happens at the function level, not within one instance. Keep `pg_pool_max` low (default 2). For high concurrency (> 50 simultaneous invocations), put **RDS Proxy** between the Lambda and Postgres:

```hcl
resource "aws_db_proxy" "stac" {
  name                   = "stac-proxy"
  engine_family          = "POSTGRESQL"
  role_arn               = aws_iam_role.proxy.arn
  vpc_subnet_ids         = aws_subnet.private[*].id
  vpc_security_group_ids = [aws_security_group.proxy.id]
  auth {
    auth_scheme = "SECRETS"
    secret_arn  = module.pgstac.secret_arn
    iam_auth    = "DISABLED"
  }
}
```

Then point `pg_dsn` at the proxy endpoint instead of the cluster endpoint.

### Schema management

PolyStac probes the schema version at startup (`SELECT pgstac.get_version()`) and refuses to start on a version older than `MinSchemaVersion`. Run `pypgstac migrate` against the database first — either via `run_pypgstac_migrate = true` on the pgstac module or manually after apply.

### `es_refresh` choice

- `wait_for` (default for ECS) — strict read-after-write at the cost of write latency.
- `false` (default for Lambda + bench) — relies on OpenSearch's periodic refresh (default 1 s). Use this for ingest paths where read-after-write isn't required.

The benchmark in `bench/REPORT.md` runs with `false`; the parity matrix in `test/parity/` runs with `wait_for`.

### Index templates

PolyStac installs two composable index templates on first start (idempotent): `polystac-items` matching `<prefix>*`, and `polystac-collections` matching the configured collections index. Migrating from `stac-server` or `stac-fastapi-elasticsearch-opensearch`? Keep the existing indices and let PolyStac install templates alongside — index names are configurable via `POLYSTAC_ES_INDEX_PREFIX` and `POLYSTAC_ES_COLLECTIONS_INDEX`.

### Logging

`POLYSTAC_LOG_FORMAT=json` (default in the modules) emits structured logs that CloudWatch Logs Insights queries cleanly. Each request: one line with `method`, `path`, `status`, `latency_ms`, `backend`.

### Tracing

Lambda module enables AWS X-Ray. PolyStac's OTel facade is wired; a real exporter is on the v1.1 roadmap.

### VPC cold start

pgstac requires VPC, which historically added 10+ seconds to Lambda cold start. With Lambda's [VPC networking improvements](https://aws.amazon.com/blogs/compute/announcing-improved-vpc-networking-for-aws-lambda-functions/) (2019) the overhead is now < 100 ms.

## 8. Verify

```sh
# Lambda
URL=$(terraform output -raw function_url)
curl -s "$URL/" | jq .id
curl -s "$URL/conformance" | jq '.conformsTo | length'

# ECS
URL=$(terraform output -raw alb_dns_name)
curl -s "http://$URL/_health"
curl -s "http://$URL/conformance" | jq '.conformsTo | length'

# Cold-start INIT_DURATION (Lambda only)
aws logs filter-log-events \
  --log-group-name /aws/lambda/polystac \
  --filter-pattern '"INIT_DURATION"' \
  --max-items 5 --query 'events[].message' --output text
```

The `bench/dropin/run.sh` harness can be pointed at the deployed ALB / Function URL to validate HTTP-level drop-in compatibility against `stac-server`:

```sh
URL=$(terraform output -raw alb_dns_name)
go run ./bench/dropin -a "http://$URL" -b http://localhost:13000
```

## 9. Migrate data

`polystac migrate` works against any pair of registered backends:

```sh
polystac migrate \
  --from pgstac --from-env DSN="$SOURCE_PG_DSN" \
  --to opensearch --to-env HOSTS="https://os.example.com" \
                   --to-env USERNAME=admin --to-env PASSWORD=$OS_PW \
  --batch-size 1000 --workers 4 \
  --resume /tmp/polystac-migrate.json \
  --sample-verify 100
```

Run from your laptop, an EC2 jumphost, or a one-off Fargate task — anywhere both endpoints are reachable.

## 10. Cost rough estimate

Moderate STAC API workload (1 req/s sustained, 86 400 invocations/day):

| | PolyStac (Go) | stac-fastapi-pgstac (Python) | stac-server (Node) |
|---|---:|---:|---:|
| Lambda memory | 256 MB | 1024 MB | 512 MB |
| GB-second/day | ~2200 | ~17 600 | ~7600 |
| Lambda compute (us-east-1, arm64) | ~$0.85 / mo | ~$11.30 / mo | ~$5.50 / mo |
| Lambda request | ~$0.50 / mo | ~$0.50 / mo | ~$0.50 / mo |

Numbers are illustrative — your traffic shape, P95 duration, and free-tier offsets dominate. The point is PolyStac's smaller memory footprint maps directly to GB-second cost.

For sustained traffic above ~10 req/s, the ECS Fargate option is typically cheaper than Lambda — one `cpu=512, memory=1024` task at ~$0.04/hr ≈ $30/month covers the same load that would cost $40+ in Lambda invocations.

## 11. SAM / CloudFormation users

The Terraform module is the source of truth. Translation is mechanical — the resource set is just `aws_lambda_function` (`provided.al2023`, `bootstrap` handler), `aws_lambda_function_url`, `aws_iam_role` + execution policies, `aws_cloudwatch_log_group`, optional `vpc_config`. ~30 lines per stack in SAM. We don't ship a SAM template because keeping two IaC representations in sync added drift faster than it added value.

## 12. Migrating from `stac-server` (Node)

PolyStac's drop-in compatibility with stac-server is validated by `bench/dropin/` — see `bench/DROPIN-REPORT.md`. The Lambda swap:

1. Build PolyStac (§1).
2. Deploy with the OpenSearch backend (§3) pointed at your existing OS domain (use `examples/lambda-byo/` with `backend = "opensearch"`).
3. Update API Gateway / CloudFront to route to the new Function URL.
4. Verify with `bench/dropin/run.sh` against your existing data.
5. Decommission the old stac-server deployment.

The OpenSearch index layout differs slightly between PolyStac and stac-server (PolyStac uses `items_<col>` directly; stac-server uses `items_<col>-000001` aliases). For a clean swap, run `polystac migrate --from opensearch --to opensearch` from the old layout to a parallel cluster with the new layout, then switch the API.

## 13. Roadmap items affecting AWS deploys

- IAM-signed OpenSearch requests (drop the username/password requirement for AWS-managed OS).
- `polystac-ingest` Lambda variant with SQS event source mapping.
- Provisioned-concurrency examples in the lambda module.
- Real OpenTelemetry → X-Ray exporter wiring.
- `pypgstac migrate` via CodeBuild / one-shot ECS task (replaces the local-exec opt-in).
