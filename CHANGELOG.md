# Changelog

All notable changes to PolyStac. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added — Gate 0 contract layer
- Repository skeleton, Go module `github.com/example/polystac` (Go 1.23+).
- `pkg/stac` STAC types (Item, Collection, Catalog, Asset, Link, ItemCollection, Geometry) with canonical-ordered `MarshalJSON` for byte-equivalence with `stac-fastapi`.
- `pkg/repository` interface, `SearchRequest`, `Page[T]`, sentinel errors (`ErrNotFound`, `ErrConflict`, `ErrInvalidInput`, `ErrNotImplemented`, `ErrBackendUnavailable`), `Capabilities`, optional sub-interfaces (`Aggregator`, `Queryables`).
- `pkg/cql2` shim over `github.com/exergy-dev/go-cql2` (text + JSON codecs side-effect-imported; AST node and Operator constants re-exported).
- `pkg/cql2/eval` AST evaluator: comparison, logical, between, in, like, isNull, temporal, bbox-spatial. Used as the in-memory backend's filter and the property-test oracle.
- `internal/backends/registry` runtime backend registry (`Register`, `Open`).
- `ARCHITECTURE.md` documenting the contract.
- CI scaffold (`.github/workflows/ci.yml`): `go build`, `go vet`, `go test -race`, `govulncheck`.

### Added — Gate 1 fronts
- **Front A (server + service layer):** stdlib `net/http` mux for STAC API Core, Collections, Features, Item Search, Queryables, Health/Ready, plus the Transaction extension. Link generation, conformance-class assembly, error normalization, OpenAPI doc stub. Middleware chain.
- **Front B (pgstac):** `pgxpool` wiring, schema-version probe (`pgstac.get_version()` ≥ `MinSchemaVersion`), `pgstac.search`-based read path, `upsert_item`/`create_items`/`delete_item` write path, `aggregate` and `get_queryables`. Honors `USE_API_HYDRATE`. Translator unit-tested.
- **Front C (OpenSearch / Elasticsearch):** lightweight HTTP `SearchClient` (covers both flavors with no SDK weight), composable index template, full CQL2-AST → DSL translator, `search_after` pagination with HMAC-encoded opaque token, `_bulk` writes. 11 translator/integration unit tests via fake client.
- **Front D (CQL2 oracle):** `pkg/cql2/eval` (above) + parser shim. Property tests in `test/cql2` use `pgregory.net/rapid` to assert encoder idempotence and eval determinism.
- **Front E (in-memory backend):** full `Repository` + `Aggregator` + `Queryables`, slice-backed, uses `pkg/cql2/eval` for filter. Test substrate for the server and parity matrix.
- **Front F (config + CLI):** env + flag loader honoring both `POLYSTAC_*` and `STAC_FASTAPI_*` names; `polystac serve` + `polystac migrate` + `polystac version`.
- **Front G (observability):** `log/slog` JSON/text handlers; Prometheus registry with `polystac_request_duration_seconds`, `polystac_repository_duration_seconds`, `polystac_backend_pool_in_use`, `polystac_hook_invocations_total`; `Tracer` facade for OTel.
- **Front H (parity + property tests):** `test/parity` generic Suite + 10-case corpus (the cross-backend behavior contract); pgstac and OS/ES wiring behind `integration` build tags. `test/cql2` rapid property tests.
- **Front I (deployment artifacts):** distroless multi-arch `Dockerfile`; `cmd/polystac-lambda` (provided.al2023); Helm chart with `values-pgstac.yaml` / `values-opensearch.yaml` overlays; Terraform module covering both backends (`docs/deploy-lambda.md` walkthrough).
- **Front J (hooks):** in-process `pkg/polystac/hook` (Pre, Post, Chain), out-of-process HTTP webhook (`internal/hooks.HTTPWebhook`) with passthrough/short-circuit/rewrite semantics.

### Added — Gate 2
- **Front K (extensions wiring):** Filter (CQL2-text + JSON), Query, Sort, Fields, Transaction, Aggregation, Queryables, Free-text — wired through Front A's request parser and per-backend translators in B/C/E.
- **Front L (migrate):** `polystac migrate -from <a> -to <b>` polyglot copy with goroutine pool, JSON-sidecar resume, and sample-verify.
- **Front M (CI conformance gates):** `.github/workflows/conformance.yml` boots the server and runs `stac-api-validator`. `.github/workflows/integration.yml` runs the parity corpus against live pgstac and OpenSearch services.
- **Front N (ingest binary):** `cmd/polystac-ingest` with stdin/dir receivers always built; SQS receiver behind `-tags aws`. Reuses backend factory and `BulkUpsertItems`.

### Added — Gate 3
- README quickstart for inmem / pgstac / OS / ES.
- `docs/migration-from-stac-fastapi.md`, `docs/migration-from-stac-server.md`, `docs/operations.md`, `docs/extending.md`.
- `load/k6/search.js` k6 load-test script targeted at the SDD §NF-1 P95 ≤ 150 ms budget.

### Build artifacts
- `polystac` (server) — ~13.8 MB static.
- `polystac-lambda` — ~15.9 MB static.
- `polystac-ingest` — ~10.8 MB static (without `-tags aws`).
- All built `CGO_ENABLED=0`, distroless-ready.

### Added — real integration tests
- `test/parity/containers_test.go` (behind `//go:build integration`) spins up pgstac, OpenSearch, and Elasticsearch via testcontainers-go. The existing parity corpus runs unchanged against each. All 10 cases pass for all three backends; tested locally with Docker on `linux/amd64`.
- `internal/backends/pgstac/translator.go`: `between(t, lo, hi)` is rewritten to `(t >= lo) AND (t <= hi)` before encoding to CQL2-JSON. Works around a dialect divergence between go-cql2 (3-arg flat form) and pgstac's `cql2_query` (which expects the bounds nested in an array). The two halves of the rewrite are accepted unchanged by both sides.
- `internal/backends/pgstac/translator.go`: `filter-lang` always set to `"cql2-json"` since the AST is always re-encoded as JSON before sending. pgstac rejects `"cql2-text"` on this field.

### Known limitations
- OpenTelemetry exporter is staged behind a `Tracer` interface; no real exporter wired yet.
- DuckDB / Parquet read-only backend not yet implemented (planned for v1.1, behind a `duckdb` build tag — would re-introduce CGO and so will be opt-in).
- Aggregation extension parity between pgstac and OpenSearch is incomplete; supported `agg_type` values differ per backend (validated at request time, returns 400 for unsupported aggs).
- Free-text search is off by default on pgstac (requires `pg_trgm`) and on by default on OpenSearch.
