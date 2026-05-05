# PolyStac — Architecture (Gate 0 Contract)

> **Status:** v0.0.1-contract — frozen during Gate 0. Changes require a contract review (see SDD §3, risk #2).

This document is the load-bearing contract every front in Gate 1 and beyond builds against. The full design is in the SDD; this file records only the rules that backends and extensions must obey at the source level.

## 1. Layering

Four horizontal layers, no skipping:

```
HTTP request
  → Routing layer        (internal/server)
  → Service layer        (internal/server, internal/app)
  → Repository interface (pkg/repository)
  → Backend impl         (internal/backends/<name>)
```

The Routing and Service layers MUST NOT import any backend package. The Backend packages MUST NOT import `internal/server`. The `pkg/` tree is leaf — it MUST NOT import `internal/`.

## 2. Canonical JSON marshalling

PolyStac aims for byte-equivalent responses with `stac-fastapi-pgstac` and `stac-fastapi-elasticsearch-opensearch` for golden-file fixtures (SDD §9.3, F-9). To get there:

- Every STAC type in `pkg/stac` defines `MarshalJSON` that emits keys in a **canonical order** — known fields first in spec order, unknown/extension fields after, alphabetical within their group.
- Item properties: `datetime`, `start_datetime`, `end_datetime`, `created`, `updated` first; remaining property keys alphabetically.
- Empty slices/maps that the spec requires are emitted as `[]`/`{}` (not `null`).
- Backends MUST construct STAC values via the types in `pkg/stac` and MUST NOT hand-marshal JSON.

The single source of truth is `pkg/stac/marshal.go` (`marshalOrdered`). Add a key to a type → add it to that type's canonical list. Tests in `pkg/stac/types_test.go` enforce the order.

## 3. Errors

- `pkg/repository/errors.go` defines the only sentinels backends return: `ErrNotFound`, `ErrConflict`, `ErrNotImplemented`, `ErrInvalidInput`, `ErrBackendUnavailable`.
- Backends MUST wrap with `fmt.Errorf("...: %w", repository.ErrX)` when surfacing a backend-native cause; the service layer relies on `errors.Is` to choose the HTTP status.
- CQL2 errors are namespaced separately: `*cql2.ParseError` for parse-time, `*cql2.TranslationError` for backend-translation failures. The service layer maps both to HTTP 400.

## 4. Optional sub-interfaces

Some features are not universally available. We use **optional sub-interfaces** (not stub methods returning `ErrNotImplemented`):

- `repository.Aggregator` — Aggregation extension.
- `repository.Queryables` — Filter extension's `/queryables`.

The service layer probes these with type assertion at startup; absent → routes are not wired and the corresponding conformance class is not advertised.

When adding a new optional capability, prefer a new sub-interface to a new field on `Capabilities` if the capability has its own RPC method.

## 5. The Repository contract

- All methods are safe for concurrent use.
- Cancellation is honored via `ctx`.
- Inputs are not mutated or retained after return.
- Backends do not log; they return errors. The middleware layer logs.

`SearchRequest` is the canonical query shape (`pkg/repository/search.go`). The service layer parses both GET and POST search formats into the same struct; backends translate that struct, never the raw HTTP request.

## 6. CQL2 boundary

- `pkg/cql2` is a thin shim over `github.com/exergy-dev/go-cql2`. It re-exports the upstream AST node types and operator constants so backends import only PolyStac.
- Backend translators implement the upstream `Visitor` interface (`cql2.Walk(v, e)`) or use a type switch on the re-exported node types. They MUST NOT re-parse CQL2.
- A `cql2.TranslationError` carries the backend name, the offending operator (if any), and a human reason.

## 7. Backend registration

`internal/backends/registry.go`. Each backend package registers a `Constructor` in its `init()`:

```go
func init() { backends.Register("pgstac", Open) }
```

`Open(ctx, *config.Config)` is the runtime entry. The registry resolves the configured backend name (`POLYSTAC_BACKEND`) at startup; mis-spelled or unregistered names fail fast with the list of known backends.

## 8. Tests

Every package ships with `go test ./...`-runnable tests. Integration tests that require Docker live behind a build tag and are run by Front H's testcontainers harness, not by `go test ./...` in plain CI.
