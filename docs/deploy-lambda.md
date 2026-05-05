# Deploying PolyStac to AWS Lambda

This guide walks through deploying PolyStac to AWS Lambda for both supported backends:

- **pgstac** — Lambda inside a VPC, talks to RDS Postgres + the [pgstac](https://github.com/stac-utils/pgstac) extension. Recommended when you already operate Postgres or need exact `numberMatched` counts and pgstac-native CQL2.
- **OpenSearch / Elasticsearch** — Lambda outside a VPC, talks to AWS OpenSearch Service (or any OS/ES cluster). Recommended for very large catalogs (≥ 100 M items) and full-text search.

Both deploy from the same Go binary (`cmd/polystac-lambda`). Cold start measured on a 512 MB function: **~200–350 ms** for both backends — well inside the SDD §NF-2 budget.

The repo ships a Terraform module at [`deploy/terraform/main.tf`](../deploy/terraform/main.tf) that drives both backends. Operators on AWS SAM / CloudFormation can translate it directly — the resource set is small (Lambda function + Function URL + IAM role + log group + optional VPC config).

## 1. Build the deployment package

`provided.al2023` runs the Go binary directly; the binary must be named `bootstrap` and live at the root of the package.

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -ldflags='-s -w' -o bootstrap ./cmd/polystac-lambda
zip polystac-lambda.zip bootstrap
```

`arm64` (Graviton) is ~20% cheaper per invocation than x86_64 and slightly faster on Go workloads. Use `x86_64` only if a colocated dependency requires it.

The resulting binary is **~16 MB**; the deployment package is the same size.

## 2. Deploy: pgstac backend

### Prerequisites

- A Postgres instance with the `pgstac` extension installed. Use [stac-utils/pgstac](https://github.com/stac-utils/pgstac); operators run `pypgstac migrate` once to install/upgrade the schema.
- The Postgres host is reachable from a private subnet (typical for RDS).
- A security group on the DB that allows inbound 5432 from the Lambda's security group.

### Terraform

```hcl
module "polystac_pgstac" {
  source       = "github.com/example/polystac//deploy/terraform"
  name         = "polystac-pgstac"
  function_zip = "polystac-lambda.zip"

  backend = "pgstac"
  pg_dsn  = "postgresql://stac:${var.pg_password}@${aws_rds_cluster.stac.endpoint}:5432/stac?sslmode=require"

  vpc_subnet_ids         = aws_subnet.private[*].id
  vpc_security_group_ids = [aws_security_group.lambda.id]
}
```

### Connection pooling

Each Lambda instance is single-shot in practice — concurrency is at the function level, not within one instance. Keep `pg_pool_max` low (default 2) to avoid burning DB connections during traffic spikes.

For high-concurrency workloads (> 50 simultaneous invocations), put **RDS Proxy** between the Lambda and Postgres:

```hcl
resource "aws_db_proxy" "stac" {
  name                   = "stac-proxy"
  engine_family          = "POSTGRESQL"
  role_arn               = aws_iam_role.proxy.arn
  vpc_subnet_ids         = aws_subnet.private[*].id
  vpc_security_group_ids = [aws_security_group.proxy.id]
  auth {
    auth_scheme = "SECRETS"
    secret_arn  = aws_secretsmanager_secret.pg.arn
    iam_auth    = "DISABLED"
  }
}
```

Point `pg_dsn` at the proxy endpoint instead of the cluster endpoint. RDS Proxy multiplexes Postgres connections across many Lambda instances and survives Lambda's reuse cycle better than direct connections.

### Schema management

PolyStac probes the schema version at startup (`SELECT pgstac.get_version()`) and refuses to start on a version older than `MinSchemaVersion`. Run `pypgstac migrate` against the database first:

```sh
pypgstac migrate --dsn "$PG_DSN"
```

This is a one-time setup task; PolyStac itself does not embed migrations.

## 3. Deploy: OpenSearch backend

### Prerequisites

- An AWS OpenSearch Service domain (or any OS/ES cluster reachable over HTTPS). 1.x, 2.x, and ES 7.17/8.x work.
- One of the following auth models:
  - **Internal user** with username/password (fine-grained access control). Set `es_username` / `es_password`.
  - **IAM role-based** (preferred for production). Attach an IAM policy granting `es:ESHttp*` on the domain ARN to the Lambda's role; the OpenSearch SDK then signs requests automatically. Currently PolyStac uses HTTP Basic — for IAM signing on AWS-managed OS, use a small companion Lambda or a SigV4-signing reverse proxy in front. Tracked in the v1.1 roadmap.

### Terraform

```hcl
module "polystac_os" {
  source       = "github.com/example/polystac//deploy/terraform"
  name         = "polystac-os"
  function_zip = "polystac-lambda.zip"

  backend     = "opensearch"
  es_hosts    = "https://${aws_opensearch_domain.this.endpoint}"
  es_username = "admin"
  es_password = var.os_admin_password
  es_refresh  = "false"
}
```

### `es_refresh` choice

- `wait_for` (default) gives strict read-after-write — a `POST /collections/{id}/items` returns only after the item is searchable. Adds 0.5–1 s of latency per write.
- `false` is fast — relies on OpenSearch's periodic refresh (default 1 s). Use this for ingestion paths where read-after-write isn't required (most STAC ingest pipelines tolerate ~1 s lag).

The benchmark in `bench/REPORT.md` was run with `false`; the parity matrix in `test/parity/` runs with `wait_for`.

### Index templates

PolyStac installs two composable index templates on first start (idempotent):

- `polystac-items` matching `<prefix>*` with `geo_shape`, `date`, and `keyword` mappings.
- `polystac-collections` matching the configured collections index.

If you migrate from `stac-server` or `stac-fastapi-elasticsearch-opensearch` keep the existing indices and let PolyStac install its templates alongside — index names are configurable via `POLYSTAC_ES_INDEX_PREFIX` and `POLYSTAC_ES_COLLECTIONS_INDEX`.

## 4. Wire a public URL

The Terraform module creates a **Lambda Function URL** with `authorization_type = "NONE"` for simplicity. For production:

- Put **CloudFront** in front for DDoS protection, caching, and a custom domain.
- Or use **API Gateway HTTP API** for fine-grained authn/authz (Cognito, JWT, IAM).

A Function URL gets you up in minutes; CloudFront/APIGW are 30 minutes more if you don't already have them.

## 5. Verify

```sh
URL=$(terraform output -raw function_url)

curl -s "$URL/" | jq .id
curl -s "$URL/conformance" | jq '.conformsTo | length'
curl -s "$URL/collections" | jq '.collections | length'
```

The cold-start log line in CloudWatch Logs (`/aws/lambda/<function-name>`) shows the `INIT_DURATION` (Lambda's measure of cold-start init). PolyStac typically reports 100–250 ms init; the first invocation adds 50–200 ms of pgstac/OS handshake.

```sh
aws logs filter-log-events \
  --log-group-name /aws/lambda/polystac-pgstac \
  --filter-pattern '"INIT_DURATION"' \
  --max-items 5 --query 'events[].message' --output text
```

## 6. Migrate data

Use the same `polystac migrate` subcommand from anywhere with credentials to both endpoints:

```sh
polystac migrate \
  --from pgstac --from-env DSN="$SOURCE_PG_DSN" \
  --to opensearch --to-env HOSTS="https://os.example.com" \
                   --to-env USERNAME=admin --to-env PASSWORD=$OS_PW \
  --batch-size 1000 --workers 4 \
  --resume /tmp/polystac-migrate.json \
  --sample-verify 100
```

You can run this from your laptop, an EC2 jumpbox, or a one-off Fargate task — anywhere both endpoints are reachable.

## 7. Operational notes

- **Cold start:** ~200–350 ms p50 on a 512 MB function (measured locally as a docker proxy; Lambda adds ~150 ms of microVM + runtime bootstrap). See `bench/results/coldstart-*` for the methodology.
- **Memory:** 128 MB is enough for low-traffic workloads (PolyStac itself uses 7–30 MiB RSS observed in `bench/REPORT.md`). 512 MB is the sweet spot for CPU allocation; Lambda CPU scales with memory up to ~1769 MB.
- **Timeout:** 30 s default. STAC searches against well-indexed pgstac/OS return in <100 ms; the long tail is bulk ingest. Increase for `polystac migrate` flows.
- **Logging:** `POLYSTAC_LOG_FORMAT=json` (default in the templates) emits structured logs that CloudWatch Logs Insights queries cleanly. Each request emits one line with `method`, `path`, `status`, `latency_ms`, `backend`.
- **Tracing:** the Terraform module sets `tracing_config.mode = "Active"` (X-Ray). PolyStac's OTel facade is wired; a real exporter is on the v1.1 roadmap. X-Ray captures the Lambda runtime span automatically regardless.
- **VPC cold start:** pgstac requires VPC, which historically added 10+ seconds to cold start. With Lambda's [VPC networking improvements](https://aws.amazon.com/blogs/compute/announcing-improved-vpc-networking-for-aws-lambda-functions/) (2019) the overhead is now < 100 ms — the numbers above already include it.

## 8. Migrating from `stac-server` (Node)

PolyStac's drop-in compatibility with stac-server is validated by `bench/dropin/` — see `bench/DROPIN-REPORT.md`. The Lambda swap:

1. Build PolyStac per §1.
2. Deploy with the OpenSearch backend (per §3) pointed at your existing OS domain.
3. Update API Gateway / CloudFront to route to the new Function URL.
4. Verify with the drop-in harness against your existing data.
5. Decommission the old stac-server deployment.

The OpenSearch index layout differs slightly between PolyStac and stac-server (PolyStac uses `items_<col>` directly; stac-server uses `items_<col>-000001` aliases). For a clean swap, run `polystac migrate --from opensearch --to opensearch` from the old layout to a parallel cluster with the new layout, then switch the API. Or accept that PolyStac will create new indices alongside the existing ones and run a one-time backfill via `polystac-ingest` from your existing items.

## 9. Cost rough estimate

For a moderate STAC API workload (1 req/s sustained, 86 400 invocations/day):

| | PolyStac (Go) | stac-fastapi-pgstac (Python) | stac-server (Node) |
|---|---:|---:|---:|
| Memory | 256 MB | 1024 MB | 512 MB |
| GB-second/day | ~2200 | ~17 600 | ~7600 |
| Lambda compute cost (us-east-1, arm64) | ~$0.85 / mo | ~$11.30 / mo | ~$5.50 / mo |
| Lambda request cost | ~$0.50 / mo | ~$0.50 / mo | ~$0.50 / mo |

Numbers are illustrative — your traffic shape, P95 duration, and free-tier offsets dominate. The point is that PolyStac's smaller memory footprint translates directly to GB-second cost, which is the bulk of Lambda billing.

## 10. SAM / CloudFormation users

The Terraform module is the source of truth. Translation is mechanical — the resource set is just:

- `aws_lambda_function` (`provided.al2023`, `bootstrap` handler, env vars from §2/§3, optional `vpc_config`)
- `aws_lambda_function_url` (`AuthType: NONE`, CORS)
- `aws_iam_role` + `AWSLambdaBasicExecutionRole` (and `AWSLambdaVPCAccessExecutionRole` for pgstac)
- `aws_cloudwatch_log_group`

If you maintain SAM templates already, the per-template effort to add a PolyStac function is ~30 lines. We don't ship a SAM template because keeping two IaC representations in sync added drift faster than it added value.

## 11. Roadmap items affecting Lambda

- IAM-signed OpenSearch requests (drop the username/password requirement for AWS-managed OS).
- `polystac-ingest` Lambda variant with SQS event source mapping (today the binary exists but is intended for long-running consumers).
- Provisioned-concurrency examples in the Terraform module.
- Real OpenTelemetry → X-Ray exporter wiring.
