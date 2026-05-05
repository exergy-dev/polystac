# Operating PolyStac

A short, opinionated guide for running PolyStac in production. Audience: operators familiar with running other STAC API servers; this doc focuses on what PolyStac does differently, not on STAC fundamentals.

## Configuration precedence

CLI flag → environment variable → defaults. There is no config file in v0.1; YAML overlay is on the roadmap (SDD §10) but not required.

Both `POLYSTAC_*` and `STAC_FASTAPI_*` env names are accepted. When both are set, `POLYSTAC_*` wins.

See `README.md` for the canonical env var table.

## Health and readiness

- `GET /_health` returns 200 if the process is alive (no backend probe).
- `GET /_ready` returns 200 only if the configured backend's `Health()` succeeds within 2 s.

Wire `/_health` to your liveness probe and `/_ready` to your readiness probe. The default Helm chart already does this.

## Observability

### Logs

Structured `log/slog`, JSON by default. One log line per request includes `method`, `path`, `status`, `latency_ms`, `backend`. Errors include the backend's native error code so you can correlate against Postgres or OpenSearch logs.

Switch to human-readable text with `POLYSTAC_LOG_FORMAT=text` (useful in dev, not in prod where log aggregators want JSON).

### Metrics

`/metrics` exposes Prometheus text:

| Metric | Labels |
|---|---|
| `polystac_request_duration_seconds` (histogram) | `route`, `method`, `status`, `backend` |
| `polystac_repository_duration_seconds` (histogram) | `method`, `backend`, `status` |
| `polystac_backend_pool_in_use` (gauge) | `backend` |
| `polystac_hook_invocations_total` (counter) | `phase`, `name`, `outcome` |

Plus the standard Go runtime collectors (`go_*`, `process_*`).

`route` is the registered pattern (e.g. `/collections/{id}`) so cardinality stays bounded.

### Tracing

OpenTelemetry wiring is staged behind the `Tracer` interface in `internal/observability`. The default is the no-op tracer; a real exporter ships with the v1.0 binary. SDD §13 documents the planned span set.

## Backend tuning

### pgstac

- `POLYSTAC_PG_POOL_MIN`, `POLYSTAC_PG_POOL_MAX`, `POLYSTAC_PG_POOL_MAX_CONN_LIFETIME`. Defaults: 2 / 20 / 1h.
- `POLYSTAC_PG_USE_API_HYDRATE=true` shifts hydration from pgstac into PolyStac (matches `stac-fastapi-pgstac`'s convention). Off by default.
- Schema migration is run separately with `pypgstac migrate`. PolyStac probes the schema version at startup and refuses to start on an incompatible version.

### OpenSearch / Elasticsearch

- One index per collection (`<prefix><collection>`), one shared index for collections.
- Index template is installed at startup (idempotent). Customize by editing the items index template directly with `PUT /_index_template/polystac-items`.
- `numberMatched` is approximate above 10 000 by default (`track_total_hits=10000`). Per-request opt-in for exact counts is on the roadmap.
- Sort fields must have a `keyword` or numeric mapping — text fields without a `.keyword` sub-field will return an OpenSearch error.

## Capacity sizing

A pure-Go binary on distroless. Defaults:

- 100m CPU request, 1 CPU limit
- 128 MiB memory request, 512 MiB limit
- HPA at 70% CPU between 2 and 8 replicas

These cover most workloads up to a few thousand QPS; tune from your load tests (`load/k6/search.js`). The SDD §NF-1 budget targets P95 ≤ 150 ms for a typical bbox+datetime+limit=10 search against a 10 M-item index.

## Lambda

`cmd/polystac-lambda` shares the `app.Build` pipeline with the long-running server. Cold start ≤ 500 ms on a 512 MB function (SDD §NF-2). Configure the same env vars; the Lambda runtime variant is `provided.al2023`.

Deployment template: `deploy/terraform/main.tf`. End-to-end walkthrough in `docs/deploy-lambda.md`.

## Hooks

Two delivery modes (SDD §7.5):

- **In-process Go hooks**: register at server build time via `App.PreHook` / `App.PostHook` (see `pkg/polystac/hook`). Lowest latency. Recommended for self-hosted deployments.
- **HTTP webhook**: configure `POLYSTAC_PRE_HOOK_URL`. Enables migration from `stac-server`'s JS Lambda hooks without rewriting them — see `docs/migration-from-stac-server.md`.

## Migration tool

`polystac migrate --from <a> --to <b>` copies between any two registered backends:

- Streams via paginated search sorted by `id` ascending — deterministic resume.
- `--resume <path>` persists per-collection cursors to a JSON sidecar; safe to interrupt and re-run.
- `--sample-verify N` re-fetches N random items per collection and diffs source vs destination.
- `--workers N` parallelizes batches.

No schema translation: both sides speak `pkg/stac` types. The unified abstraction is the single biggest payoff of PolyStac (SDD §11).

## Backups

PolyStac is stateless. Backups are the backend's responsibility:

- pgstac → standard Postgres backup (pg_dump, WAL archiving).
- OpenSearch → snapshot to S3 / GCS via the cluster's snapshot repository.

If you need a portable on-disk dump, run `polystac migrate --to inmem` is *not* a viable backup target (data lives in process memory). Future versions may add a JSON-Lines export sink.

## Upgrading

PolyStac follows semver (`v0`, `v1`, …). Within a minor version, the wire contract is stable. Backend schema requirements are pinned in the binary (`internal/backends/pgstac/MinSchemaVersion`, OS index template version) and surface as a startup error if violated.

The conformance class set is recomputed at every startup from the backend's reported `Capabilities` and the optional sub-interfaces (`Aggregator`, `Queryables`) it implements; downgrading a backend may shrink the advertised set.

## Security posture

- TLS termination is the load balancer's job; PolyStac listens on plain HTTP behind it.
- Backend connections (pgstac, OS/ES) require TLS by default in production config.
- AuthN/AuthZ is out of scope for the core. Wire it via a pre-hook (the OAuth 2.0 reference hook is shipped in a sibling module).
- Tenant isolation: implement as a pre-hook that injects a CQL2 clause into the parsed request before translation.
- `govulncheck` runs in CI; releases ship an SBOM.
