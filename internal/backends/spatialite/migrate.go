//go:build cgo && spatialite

package spatialite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SchemaVersion is bumped whenever a migrations/0NNN_*.sql file is added.
const SchemaVersion = 2

// runMigrations brings the schema up to SchemaVersion. Concurrent
// first-starts against the same DB file serialize on busy_timeout;
// later starters read MAX(version) and skip already-applied entries.
func runMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS polystac_schema (
            version    INTEGER PRIMARY KEY,
            applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
        )`); err != nil {
		return fmt.Errorf("spatialite: bootstrap polystac_schema: %w", err)
	}

	cur, err := currentSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if cur > SchemaVersion {
		return fmt.Errorf("spatialite: db schema v%d > binary v%d (downgrade not supported)", cur, SchemaVersion)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("spatialite: read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		v := versionFromFilename(e.Name())
		if v <= 0 || v <= cur {
			continue
		}
		body, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		if err != nil {
			return fmt.Errorf("spatialite: read %s: %w", e.Name(), err)
		}
		if err := applyMigration(ctx, db, v, string(body)); err != nil {
			return fmt.Errorf("spatialite: apply %s: %w", e.Name(), err)
		}
	}

	return ensureGeometryAndIndex(ctx, db)
}

func applyMigration(ctx context.Context, db *sql.DB, version int, body string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO polystac_schema(version) VALUES (?)`, version); err != nil {
		return err
	}
	return tx.Commit()
}

func currentSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM polystac_schema`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("spatialite: read schema version: %w", err)
	}
	return int(v.Int64), nil
}

func versionFromFilename(name string) int {
	i := strings.IndexByte(name, '_')
	if i <= 0 {
		return 0
	}
	n, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0
	}
	return n
}

// ensureGeometryAndIndex installs the SpatiaLite geometry column and
// R-Tree on the items table. Idempotent; safe to call on every Open.
func ensureGeometryAndIndex(ctx context.Context, db *sql.DB) error {
	// InitSpatialMetadata is required once per database. The "1"
	// argument selects the WGS84-only fast path (skips the large SRS
	// dictionary). On an already-populated DB it returns NULL rather
	// than erroring; tolerate that by probing spatial_ref_sys.
	if _, err := db.ExecContext(ctx, `SELECT InitSpatialMetadata(1)`); err != nil {
		var n int
		if probe := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM spatial_ref_sys`).Scan(&n); probe != nil {
			return fmt.Errorf("spatialite: InitSpatialMetadata: %w", err)
		}
	}

	var hasGeom int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM geometry_columns
         WHERE lower(f_table_name)='items' AND lower(f_geometry_column)='geom'`,
	).Scan(&hasGeom); err != nil {
		return fmt.Errorf("spatialite: probe geometry_columns: %w", err)
	}
	if hasGeom == 0 {
		if _, err := db.ExecContext(ctx,
			`SELECT AddGeometryColumn('items','geom',4326,'GEOMETRY','XY',0)`,
		); err != nil {
			return fmt.Errorf("spatialite: AddGeometryColumn: %w", err)
		}
	}

	var hasIdx int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master
         WHERE type='table' AND name='idx_items_geom'`,
	).Scan(&hasIdx); err != nil {
		return fmt.Errorf("spatialite: probe rtree: %w", err)
	}
	if hasIdx == 0 {
		if _, err := db.ExecContext(ctx,
			`SELECT CreateSpatialIndex('items','geom')`,
		); err != nil {
			return fmt.Errorf("spatialite: CreateSpatialIndex: %w", err)
		}
	}
	return nil
}
