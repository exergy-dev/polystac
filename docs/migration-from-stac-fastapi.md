# Migration: `stac-fastapi-*` → PolyStac

PolyStac aims for HTTP-level drop-in compatibility with `stac-fastapi-pgstac` and `stac-fastapi-elasticsearch-opensearch`. The wire contract — paths, query parameters, response shapes, and `STAC_FASTAPI_*` environment variables — is preserved. Source-level imports of `stac_fastapi` packages are not.

## Compatibility checklist

| Area | Drop-in | Notes |
|---|---|---|
| URL paths and methods | ✓ | every endpoint listed in SDD §9.1 |
| Query parameters (bbox, datetime, filter, sortby, fields, query, limit, token) | ✓ | parsed into `repository.SearchRequest` |
| Response shape (`Item`, `Collection`, `ItemCollection`) | ✓ | `pkg/stac` enforces canonical key ordering |
| Conformance class set | ✓ | computed at startup from `Capabilities` + sub-interface probes |
| `STAC_FASTAPI_*` env vars | ✓ | accepted alongside `POLYSTAC_*`; both work |
| Pagination tokens | ✓ | opaque on GET, `next`/`prev` body links on POST |
| Pre/post hooks | ✓ | in-process Go (`pkg/polystac/hook`) or HTTP webhook (`internal/hooks`) |

Documented small differences live in SDD §9.5.

## Migration recipe

### From `stac-fastapi-pgstac`

```sh
# 1. Build PolyStac.
go build -o polystac ./cmd/polystac

# 2. Reuse your existing pgstac DSN. Both env-var names work.
export DATABASE_URL=postgresql://stac:stac@db:5432/stac        # stac-fastapi convention
export POLYSTAC_PG_DSN=$DATABASE_URL                           # canonical
export STAC_FASTAPI_TITLE="My STAC"                            # honored
export STAC_FASTAPI_DESCRIPTION="Catalog"                      # honored
export STAC_FASTAPI_LANDING_ID=my-stac                         # honored
export POLYSTAC_BACKEND=pgstac

./polystac serve
```

PolyStac probes pgstac's schema version at startup (`pgstac.get_version()`) and refuses to start on a version older than `MinSchemaVersion` (`internal/backends/pgstac/pgstac.go`). Run `pypgstac migrate` if needed — PolyStac does not embed migrations.

### From `stac-fastapi-elasticsearch-opensearch`

```sh
export POLYSTAC_BACKEND=opensearch              # or "elasticsearch"
export POLYSTAC_ES_HOSTS=https://es:9200
export POLYSTAC_ES_USERNAME=elastic
export POLYSTAC_ES_PASSWORD=...
export POLYSTAC_ES_INDEX_PREFIX=items_          # match your existing prefix
export POLYSTAC_ES_COLLECTIONS_INDEX=collections
./polystac serve
```

PolyStac installs the items index template and collections index on first boot (idempotent). Existing indices that already match the template are left alone.

## Verification

For every backend pair you care about, run the parity matrix (`test/parity`):

```sh
# Boot PolyStac under your existing data, then run the corpus from a separate
# Go test:
go test -v ./test/parity/...
```

Add cases to `test/parity/corpus.go` for any client-specific request shape that matters. The matrix is the contract.

## Migrating data between backends

`polystac migrate` performs a polyglot copy with sample-verify:

```sh
./polystac migrate \
  --from pgstac --from-env DSN=postgresql://... \
  --to opensearch --to-env HOSTS=https://es:9200 \
                   --to-env USERNAME=admin --to-env PASSWORD=... \
  --batch-size 1000 --workers 4 \
  --resume /var/state/migrate.json \
  --sample-verify 50
```

The unified `Repository` abstraction means migration is purely an I/O exercise — no schema translation, no field mapping.

## Rollback

PolyStac does not modify your data on read. Pointing `stac-fastapi-*` back at the same datastore restores the previous serving plane immediately. For OpenSearch, the index template PolyStac installed remains; remove it with `DELETE /_index_template/polystac-items` if you want to revert to your previous template.
