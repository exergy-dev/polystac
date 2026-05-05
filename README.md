# PolyStac

A polyglot [STAC API](https://github.com/radiantearth/stac-api-spec) server in Go. One static binary, swappable storage backend (pgstac / Elasticsearch / OpenSearch / in-memory), HTTP-level drop-in for [`stac-fastapi`](https://github.com/stac-utils/stac-fastapi) and [`stac-server`](https://github.com/stac-utils/stac-server).

> **Status:** v0.1 implementation in progress. See `ARCHITECTURE.md` for the contract. Single static binary (~13 MB), distroless image, no CGO.

## Quickstart

### In-memory (no setup)

```sh
go build -o polystac ./cmd/polystac
./polystac serve -listen :8000 -log-format text

curl http://localhost:8000/
curl http://localhost:8000/conformance
```

### pgstac

You need a Postgres with the `pgstac` extension installed (use [stac-utils/pgstac](https://github.com/stac-utils/pgstac); operators run `pypgstac migrate` to install/update the schema).

```sh
export POLYSTAC_BACKEND=pgstac
export POLYSTAC_PG_DSN=postgresql://stac:stac@db:5432/stac
./polystac serve
```

### OpenSearch / Elasticsearch

```sh
export POLYSTAC_BACKEND=opensearch    # or elasticsearch
export POLYSTAC_ES_HOSTS=https://opensearch-master:9200
export POLYSTAC_ES_USERNAME=admin
export POLYSTAC_ES_PASSWORD=...
./polystac serve
```

PolyStac creates the items index template and collections index on first start (idempotent).

## Subcommands

```
polystac serve     run the HTTP server (default)
polystac migrate   copy data between any two backends
polystac version
polystac help
```

`polystac migrate -from pgstac -to opensearch -batch-size 1000 -sample-verify 50` works against any pair of supported backends — there is no schema translation, only I/O.

A companion `polystac-ingest` binary streams items from stdin, a directory of JSON files, or SQS (build with `-tags aws`) into the configured backend.

## Configuration

All configuration is via environment variables (with optional CLI flag override). PolyStac honors both `POLYSTAC_*` (canonical) and `STAC_FASTAPI_*` (alias) names so existing `stac-fastapi` deployments can switch to PolyStac without renaming variables.

See SDD §10 for the full surface; common keys:

| Variable | Default | Purpose |
|---|---|---|
| `POLYSTAC_BACKEND` | `inmem` | one of `inmem`, `pgstac`, `opensearch`, `elasticsearch`, `spatialite` (CGO build) |
| `POLYSTAC_LISTEN` | `:8000` | listen address |
| `POLYSTAC_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `POLYSTAC_LOG_FORMAT` | `json` | `json` or `text` |
| `POLYSTAC_DEFAULT_LIMIT` | `10` | search default page size |
| `POLYSTAC_MAX_LIMIT` | `10000` | search max page size |
| `POLYSTAC_PG_DSN` | — | pgstac connection string |
| `POLYSTAC_ES_HOSTS` | — | OS/ES URL (comma-separated) |
| `POLYSTAC_ES_USERNAME` / `POLYSTAC_ES_PASSWORD` | — | OS/ES credentials |
| `POLYSTAC_ES_INDEX_PREFIX` | `items_` | per-collection items index prefix |
| `POLYSTAC_ES_COLLECTIONS_INDEX` | `collections` | shared collections index |
| `POLYSTAC_SPATIALITE_DATABASE` | — | SpatiaLite DB file path (CGO build) |
| `POLYSTAC_SPATIALITE_EXTENSION_PATH` | `mod_spatialite` | override mod_spatialite library name/path |

### SpatiaLite backend (optional, CGO build)

The default `polystac` binary is pure Go (CGO-free, NF-7). The SpatiaLite backend depends on CGO and the `mod_spatialite` shared library, so it ships as a separate `polystac-spatialite` artifact built with `-tags 'cgo spatialite'`. Install the extension first (Debian: `apt install libsqlite3-mod-spatialite`; Alpine: `apk add libspatialite`).

```sh
CGO_ENABLED=1 go build -tags 'cgo spatialite' -o polystac-spatialite ./cmd/polystac
POLYSTAC_BACKEND=spatialite POLYSTAC_SPATIALITE_DATABASE=/var/lib/polystac/stac.db ./polystac-spatialite serve
```

`Dockerfile.spatialite` produces a container image with the extension preinstalled. The backend creates its own schema (collections, items, R-Tree spatial index) on first open.

## Deployment

- **Container:** `Dockerfile` builds a distroless multi-arch image (~25–30 MB).
- **Kubernetes:** `deploy/helm/polystac` Helm chart with overlay values for pgstac (`values-pgstac.yaml`) and OpenSearch (`values-opensearch.yaml`).
- **AWS Lambda or ECS Fargate:** `deploy/terraform/` ships a composable module set — `modules/{lambda,server,pgstac,opensearch}` plus five `examples/*` stacks (lambda-byo, lambda-pgstac, lambda-opensearch, server-pgstac, server-opensearch). Cold start < 500 ms. End-to-end walkthrough in `docs/deploy-aws.md`.

## Drop-in migration

### From `stac-fastapi-pgstac`

1. Point your existing pgstac at PolyStac:
   ```sh
   POLYSTAC_BACKEND=pgstac POLYSTAC_PG_DSN=$DATABASE_URL polystac serve
   ```
2. PolyStac honors the same `STAC_FASTAPI_*` env vars (title, description, root path, …).
3. Diff response shapes against the corpus in `test/parity/corpus.go` if you have custom clients.

### From `stac-fastapi-elasticsearch-opensearch`

Same shape — set `POLYSTAC_BACKEND=opensearch` (or `elasticsearch`), `POLYSTAC_ES_HOSTS`, credentials.

### From `stac-server` (Node.js, Lambda + SNS/SQS)

Replace the API Lambda with `cmd/polystac-lambda` (same API Gateway integration). Replace the ingest Lambda with `cmd/polystac-ingest` built with `-tags aws`, pointed at your existing SQS queue. Existing SNS topics, DLQs, and subscriptions stay in place.

Pre/post hooks written in JavaScript: register them as HTTP webhooks via `POLYSTAC_PRE_HOOK_URL` / `POLYSTAC_POST_HOOK_LAMBDA_ARN` (in-process Go hooks are also supported, see `pkg/polystac/hook`).

## Layout

```
cmd/polystac/         server binary
cmd/polystac-lambda/  AWS Lambda variant
cmd/polystac-ingest/  SQS / stdin / dir ingest companion
pkg/stac/             STAC types with canonical-ordered JSON
pkg/repository/       Repository interface, SearchRequest, capabilities
pkg/cql2/             shim over github.com/exergy-dev/go-cql2
pkg/cql2/eval/        AST evaluator (in-memory + property-test oracle)
pkg/polystac/hook/    in-process hook API
internal/server/      HTTP routing + service layer
internal/app/         wiring
internal/backends/    inmem | pgstac | opensearch | spatialite (cgo+spatialite tags)
internal/config/      env + flag loader
internal/observability/ slog + Prometheus + tracing facade
internal/hooks/       HTTP webhook delivery
internal/migrate/     migrate subcommand
internal/ingest/      ingest pipeline
test/parity/          cross-backend parity matrix
test/cql2/            rapid property tests
deploy/{helm,sam,terraform}/  deployment artifacts
load/k6/              k6 load-test scripts
```

## Build & test

```sh
go build ./...
go test ./...                              # unit + parity (inmem)
go test -tags 'integration pgstac' ./...        # spins up pgstac via testcontainers
go test -tags 'integration opensearch' ./...    # spins up OpenSearch via testcontainers
go test -tags 'integration elasticsearch' ./... # spins up Elasticsearch 8.x via testcontainers
go test -tags 'integration pgstac opensearch elasticsearch' ./...  # all of them
CGO_ENABLED=1 go test -tags 'cgo spatialite' ./internal/backends/spatialite/...  # SpatiaLite unit tests
CGO_ENABLED=1 go test -tags 'cgo spatialite' ./test/parity/...                  # parity corpus on SpatiaLite
```

The integration tags require Docker on the host. Set `POLYSTAC_TEST_PG_DSN` / `POLYSTAC_TEST_ES_HOSTS` (with optional `_USERNAME`/`_PASSWORD`) to point at an already-running cluster and skip the container spin-up. SpatiaLite tests skip cleanly when `mod_spatialite` is not on the loader path.
