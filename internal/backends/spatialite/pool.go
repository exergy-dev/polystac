//go:build cgo && spatialite

package spatialite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/mattn/go-sqlite3"
)

// dbConfig is the small subset of SpatiaLite knobs PolyStac surfaces via
// configuration. Anything else can be set in the DSN.
type dbConfig struct {
	Database      string // file path (required)
	PoolMax       int    // sql.DB.MaxOpenConns; 1 enforces single-writer regime
	CacheKB       int    // PRAGMA cache_size=-<n> (KiB); 0 = driver default
	MmapSizeBytes int64  // PRAGMA mmap_size; default 256 MiB
	BusyTimeoutMS int    // _busy_timeout query param; default 5000
	ExtensionPath string // overrides the mod_spatialite library name/path
}

// configFromMap pulls dbConfig from the lowercase backend env subtree
// produced by internal/config (POLYSTAC_SPATIALITE_* → keys without the
// prefix).
func configFromMap(env map[string]string) (dbConfig, error) {
	c := dbConfig{
		PoolMax:       1,
		BusyTimeoutMS: 5000,
		MmapSizeBytes: 256 << 20,
		ExtensionPath: "mod_spatialite",
	}
	c.Database = env["database"]
	if c.Database == "" {
		return c, errors.New("spatialite: POLYSTAC_SPATIALITE_DATABASE is required")
	}
	if v := env["pool_max"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("spatialite: pool_max %q: %w", v, err)
		}
		if n < 1 {
			return c, fmt.Errorf("spatialite: pool_max must be >= 1 (got %d)", n)
		}
		c.PoolMax = n
	}
	if v := env["cache_kb"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("spatialite: cache_kb %q: %w", v, err)
		}
		c.CacheKB = n
	}
	if v := env["mmap_size"]; v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return c, fmt.Errorf("spatialite: mmap_size %q: %w", v, err)
		}
		c.MmapSizeBytes = n
	}
	if v := env["busy_timeout_ms"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("spatialite: busy_timeout_ms %q: %w", v, err)
		}
		c.BusyTimeoutMS = n
	}
	if v := env["extension_path"]; v != "" {
		c.ExtensionPath = v
	}
	return c, nil
}

// Each (extensionPath) gets its own database/sql driver name registered
// once via sync.Once so concurrent Open() calls don't race on
// sql.Register (which panics on duplicates).
var (
	driverMu       sync.Mutex
	driverRegistry = map[string]string{} // extensionPath → driver name
	driverSeq      int
)

// registerDriver returns a database/sql driver name configured to load
// the given mod_spatialite library on every new connection. Subsequent
// calls with the same path return the cached name.
func registerDriver(extPath string) string {
	driverMu.Lock()
	defer driverMu.Unlock()
	if name, ok := driverRegistry[extPath]; ok {
		return name
	}
	driverSeq++
	name := fmt.Sprintf("sqlite3_polystac_%d", driverSeq)
	sql.Register(name, &sqlite3.SQLiteDriver{
		ConnectHook: func(c *sqlite3.SQLiteConn) error {
			// Load mod_spatialite on every new connection. The empty
			// entrypoint string lets SQLite pick the default
			// (sqlite3_modspatialite_init).
			return c.LoadExtension(extPath, "")
		},
	})
	driverRegistry[extPath] = name
	return name
}

// openDB opens the SpatiaLite database with WAL mode, foreign keys on,
// and a configured busy timeout. The returned *sql.DB owns the
// underlying connections; the caller must Close it on shutdown.
func openDB(_ context.Context, dc dbConfig) (*sql.DB, error) {
	driver := registerDriver(dc.ExtensionPath)
	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_busy_timeout=%d&_foreign_keys=on&_synchronous=NORMAL&cache=shared",
		dc.Database, dc.BusyTimeoutMS,
	)
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("spatialite: open %q: %w", dc.Database, err)
	}
	db.SetMaxOpenConns(dc.PoolMax)
	db.SetMaxIdleConns(dc.PoolMax)
	db.SetConnMaxLifetime(0)
	return db, nil
}

// applyPragmas runs the post-open PRAGMA tuning that depends on
// dbConfig. Run after Open + extension load + migrations.
func applyPragmas(ctx context.Context, db *sql.DB, dc dbConfig) error {
	if dc.CacheKB > 0 {
		// Negative argument to cache_size is in KiB; positive is in pages.
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA cache_size=-%d`, dc.CacheKB)); err != nil {
			return fmt.Errorf("spatialite: pragma cache_size: %w", err)
		}
	}
	if dc.MmapSizeBytes > 0 {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA mmap_size=%d`, dc.MmapSizeBytes)); err != nil {
			return fmt.Errorf("spatialite: pragma mmap_size: %w", err)
		}
	}
	return nil
}

// pingExtension verifies mod_spatialite is loaded by calling a function
// only the extension provides. Returns a clear error pointing operators
// at the missing shared library.
func pingExtension(ctx context.Context, db *sql.DB) error {
	var v string
	if err := db.QueryRowContext(ctx, `SELECT spatialite_version()`).Scan(&v); err != nil {
		return fmt.Errorf("spatialite: mod_spatialite probe failed: %w (install libsqlite3-mod-spatialite or set POLYSTAC_SPATIALITE_EXTENSION_PATH)", err)
	}
	return nil
}
