# Extending PolyStac

Three extension paths, in increasing depth. None require forking.

## 1. Hooks (in-process or out-of-process)

Mirror `stac-server`'s pre/post Lambda hook concept but as standard Go middleware. See `pkg/polystac/hook`. Use cases: tenant filtering, asset-URL presigning, response redaction, custom auth.

```go
import (
    "net/http"
    "github.com/example/polystac/pkg/polystac/hook"
)

func main() {
    // ...
    app.PreHook(hook.PreFunc(func(w http.ResponseWriter, r *http.Request) *http.Request {
        // Reject anonymous requests for /collections/private/*
        if strings.HasPrefix(r.URL.Path, "/collections/private/") && r.Header.Get("Authorization") == "" {
            http.Error(w, "auth required", 401)
            return nil // short-circuits the chain
        }
        return r
    }))
    app.PostHook(hook.PostFunc(func(headers http.Header, body []byte) []byte {
        return rewriteAssets(body) // e.g. presign s3:// URLs
    }))
}
```

For language-agnostic hooks, set `POLYSTAC_PRE_HOOK_URL` to point at any HTTP endpoint and use `internal/hooks.HTTPWebhook`. The wire format is documented in that file.

## 2. Custom backend

Implement `repository.Repository` (and optionally `Aggregator` / `Queryables`) in your own package, then register it in `init()`:

```go
package mybackend

import (
    "github.com/example/polystac/internal/backends"
    "github.com/example/polystac/pkg/repository"
)

func init() { backends.Register("mybackend", Open) }

func Open(ctx context.Context, cfg any) (repository.Repository, error) {
    // cfg is *config.Config; read your knobs from cfg.BackendConfig
    return &Repo{ /* ... */ }, nil
}

type Repo struct{ /* ... */ }

func (r *Repo) Capabilities() repository.Capabilities { /* ... */ }
func (r *Repo) GetCollection(ctx context.Context, id string) (*stac.Collection, error) { /* ... */ }
// ... other Repository methods
```

Then add a side-effect import in your binary:

```go
import _ "yourorg/mybackend"
```

You inherit:

- The full HTTP surface (server, conformance, links).
- Configuration loading (env + flags).
- Observability (slog request log, Prometheus latency metric per route+backend).
- Migration tool (your backend can be a `--from` or `--to`).
- Parity matrix harness (`test/parity/Suite{Open: ...}.Run(t)`).

The minimum surface is the `Repository` interface (`pkg/repository/repository.go`). Everything else is optional and probed at runtime (see `ARCHITECTURE.md` §4).

## 3. Custom CQL2 → backend translator

If your backend's query language differs from pgstac's JSONB or OpenSearch's DSL, write a translator in your backend package. Walk the AST via the upstream Visitor:

```go
func translateFilter(e cql2.Expression) (MyBackendQuery, error) {
    switch n := e.(type) {
    case *cql2.Op:
        switch n.Op {
        case cql2.OpEq:
            // ...
        case cql2.OpAnd:
            // recurse on n.Args
        }
    case *cql2.PropertyRef:
        // ...
    case *cql2.NumLit, *cql2.StringLit, *cql2.BoolLit:
        // ...
    }
    // Unsupported? Return *cql2.TranslationError so the service layer
    // maps it to a 400 with a precise reason.
    return MyBackendQuery{}, &cql2.TranslationError{Backend: "mybackend", Op: n.Op, Reason: "..."}
}
```

The opensearch backend (`internal/backends/opensearch/translator.go`) is the most complete reference; pgstac's translator is mostly a passthrough since pgstac accepts CQL2-JSON natively.

## Adding parity-matrix cases

Cases live in `test/parity/corpus.go`. Adding one means making every backend pass it. This is intentional: the corpus is the cross-backend behavior contract.

```go
{
    Name: "my-new-case",
    Request: repository.SearchRequest{
        Filter:     mustParse(`my_field > 42`),
        FilterLang: repository.FilterLangText,
        Limit:      100,
    },
    WantIDs:          []string{"a-1"},
    WantMatched:      ptrI64(1),
    OrderInsensitive: true,
    SkipIf:           func(c repository.Capabilities) bool { return !c.SupportsFilterCQL2Text },
    Reason:           "backend lacks CQL2 filter support",
},
```

## Optional: contribute a STAC-API extension

Each STAC API extension is a self-contained set of routes, request fields, and conformance classes. PolyStac wires extensions in `internal/server/server.go` and parses extension-specific request fields in `internal/server/parse.go`. Adding a new extension typically means:

1. Add a route handler.
2. Extend `SearchRequest` (or the relevant domain type) with the extension's input.
3. Extend the per-backend translators to honor the new field.
4. Declare the new conformance class in `internal/server/conformance.go` gated on a `Capabilities` flag or an optional sub-interface.

Adding a single extension touches at most three files per supported backend — small surface, well-bounded.
