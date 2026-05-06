// Package spatialite implements the Repository interface against a
// SpatiaLite (SQLite + spatial extension) datastore.
//
// Build constraints:
//
//   - Compiles only under `-tags 'cgo spatialite'`. Every file in this
//     package (except this doc.go) carries that build tag so the default
//     pure-Go polystac binary remains CGO-free per NF-7.
//   - Requires the `mod_spatialite` shared library at runtime
//     (Debian: `apt install libsqlite3-mod-spatialite`,
//     Alpine: `apk add libspatialite`). Override the library name/path
//     via POLYSTAC_SPATIALITE_EXTENSION_PATH if it is not on the loader
//     search path.
//
// Schema ownership:
//
//   - PolyStac owns the schema; on first open the backend creates
//     tables, indexes, the SpatiaLite GEOMETRY column on `items.geom`,
//     and the R-Tree spatial index. Schema version is tracked in a
//     `polystac_schema` table; on a higher-than-binary version the
//     backend refuses to start.
//
// Concurrency:
//
//   - WAL mode + `_busy_timeout=5000` give multi-reader, single-writer
//     semantics. v1 uses `MaxOpenConns=1`, which is sufficient for the
//     local-dev / single-binary use case the backend targets. A future
//     enhancement is a reader/writer DB split.
//
// Out of scope for v1: the Aggregator sub-interface. Implement on
// demand using SQLite GROUP BY / aggregate functions.
package spatialite
