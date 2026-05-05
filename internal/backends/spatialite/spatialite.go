//go:build cgo && spatialite

package spatialite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/example/polystac/internal/backends"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/pkg/repository"
)

const backendName = "spatialite"

func init() {
	backends.Register(backendName, Open)
}

// Open is the registry constructor. It reads the POLYSTAC_SPATIALITE_*
// subtree from the *config.Config, opens the database, loads
// mod_spatialite, runs migrations, and returns the Repository.
func Open(ctx context.Context, anyCfg any) (repository.Repository, error) {
	cfg, ok := anyCfg.(*config.Config)
	if !ok {
		return nil, fmt.Errorf("spatialite: expected *config.Config, got %T", anyCfg)
	}
	dc, err := configFromMap(cfg.BackendConfig)
	if err != nil {
		return nil, err
	}
	db, err := openDB(ctx, dc)
	if err != nil {
		return nil, err
	}
	if err := pingExtension(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := runMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("spatialite: migrate: %w", err)
	}
	if err := applyPragmas(ctx, db, dc); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Repo{db: db, cfg: dc}, nil
}

// Repo implements repository.Repository against SpatiaLite.
type Repo struct {
	db  *sql.DB
	cfg dbConfig
}

// Capabilities reports the SpatiaLite feature set.
func (r *Repo) Capabilities() repository.Capabilities {
	return repository.Capabilities{
		Backend:                  backendName,
		SupportsTransactions:     true,
		SupportsBulkTransactions: true,
		SupportsFreeTextSearch:   false,
		SupportsFilterCQL2Text:   true,
		SupportsFilterCQL2JSON:   true,
		SupportedSortFields:      repository.SortFieldsAll,
		CountSemantics:           repository.CountExact,
		MaxItemLimit:             10000,
		Notes: []string{
			"requires CGO build and mod_spatialite shared library at runtime",
			"single-writer regime; concurrent writers wait on busy_timeout",
			"exact COUNT(*) re-runs the WHERE; expensive on large filters",
		},
	}
}

// Health is a cheap connectivity probe.
func (r *Repo) Health(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

// Close releases the underlying connection pool.
func (r *Repo) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}
