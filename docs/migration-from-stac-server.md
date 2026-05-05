# Migration: `stac-server` (Node.js) → PolyStac

`stac-server` deploys as two AWS Lambdas: an API function behind API Gateway and an ingest function consuming SNS/SQS. PolyStac mirrors the deployment shape with two binaries — `polystac-lambda` and `polystac-ingest` — so the topology around them (subscriptions, DLQs, IAM roles) stays in place.

## Topology mapping

| stac-server | PolyStac |
|---|---|
| API Lambda | `cmd/polystac-lambda` (`provided.al2023` runtime) |
| Ingest Lambda | `cmd/polystac-ingest` built with `-tags aws` |
| OpenSearch cluster | unchanged; PolyStac talks to it directly via `POLYSTAC_ES_*` |
| SNS topic + SQS queue + DLQ | unchanged; ingest binary reads from the same queue |
| Pre/post Lambda hooks (JS) | re-target via HTTP webhook (`POLYSTAC_PRE_HOOK_URL`) or rewrite as in-process Go hooks |
| `ENABLE_INGEST_ACTION_TRUNCATE` | guarded admin endpoint (Front F) — same env var name |

Everything else (CloudFront, WAF, custom domains) is untouched.

## Recipe

```sh
# 1. Build both binaries for Lambda (provided.al2023 → "bootstrap").
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags='-s -w' -o bootstrap-api    ./cmd/polystac-lambda
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags='-s -w' -tags aws -o bootstrap-ingest ./cmd/polystac-ingest

# 2. Update each Lambda function's deployment package: replace the JS
# bundle with the Go `bootstrap` binary, and switch runtime to
# provided.al2023. Keep the function ARNs, SNS topics, and DLQ ARNs.

# 3. Set environment on both functions:
#    POLYSTAC_BACKEND=opensearch
#    POLYSTAC_ES_HOSTS=https://your-cluster
#    POLYSTAC_ES_USERNAME / POLYSTAC_ES_PASSWORD
#    POLYSTAC_ES_INDEX_PREFIX=items_   (match stac-server's convention)
#    POLYSTAC_ES_COLLECTIONS_INDEX=collections

# 4. For the ingest function, add the SQS source mapping if it is not
# already there (existing stac-server deployments will have it). The
# ingest binary's --source flag must be `sqs:<queue-url>`.

# 5. Cold-start budget: SDD §NF-2 sets ≤500 ms. Validate with `aws logs`
# after first invocation.
```

`deploy/sam/template.yaml` and `deploy/terraform/main.tf` are ready-to-edit templates if you want a clean redeploy.

## Hooks

`stac-server` lets operators inject pre/post Lambda hooks written in JavaScript. PolyStac supports two replacements:

1. **Out-of-process HTTP webhook** (`internal/hooks.HTTPWebhook`). Configure with `POLYSTAC_PRE_HOOK_URL` / `POLYSTAC_POST_HOOK_URL`. The webhook receives a JSON envelope and may rewrite, short-circuit (any non-200 status), or pass through (HTTP 204). This lets existing JS hooks continue running on AWS Lambda — point the webhook URL at the existing function URL.

2. **In-process Go hook** (`pkg/polystac/hook.PreFunc`, `PostFunc`). Lower latency, requires recompilation, recommended for self-hosted deployments where you control the binary.

```go
// In a fork of cmd/polystac (or by writing your own main):
import (
    polystac "github.com/example/polystac/pkg/polystac/hook"
    "yourorg/tenancy"
    "yourorg/presign"
)

app.PreHook(polystac.PreFunc(tenancy.FilterByTenant))
app.PostHook(polystac.PostFunc(presign.AssetURLs(s3Cfg)))
```

## SNS / SQS

PolyStac deliberately does not own the SNS topic or SQS queue. Operators keep their existing infrastructure — the only thing that changes is who consumes the queue. The ingest binary deletes messages it successfully processes; failures stay in flight (and eventually go to your existing DLQ).

## Validating the swap

1. Run the conformance gate (`.github/workflows/conformance.yml`) against the new function URL — it boots stac-api-validator over HTTP, no in-process integration.
2. Run a sample-verify migration (`polystac migrate --sample-verify 100 …`) from your existing OpenSearch into a parallel cluster to confirm read-fidelity. Cancel mid-way; the resume token (`--resume <path>`) lets you pick up later.
3. Diff a sample of search responses byte-for-byte against `stac-server`'s output. SDD §9.5 documents the small, deliberate differences.

## Rollback

The pre-cutover Lambda functions remain in place if you redeploy by version-publishing rather than overwriting `$LATEST`. Aliases let you flip the API Gateway integration back in one update.
