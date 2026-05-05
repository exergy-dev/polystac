//go:build integration && pgstac

package parity_test

import (
	"context"
	"testing"

	"github.com/example/polystac/internal/backends"
	_ "github.com/example/polystac/internal/backends/pgstac"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/test/parity"
)

// TestPgstacParity runs the parity corpus against pgstac. By default it
// spins up a fresh pgstac container via testcontainers; set
// POLYSTAC_TEST_PG_DSN to point at an already-running instance and skip
// the spin-up.
func TestPgstacParity(t *testing.T) {
	dsn := pgstacDSN(t)
	parity.Suite{
		Name: "pgstac",
		Open: func(t *testing.T) repository.Repository {
			cfg := config.Defaults()
			cfg.Backend = "pgstac"
			cfg.BackendConfig = map[string]string{"dsn": dsn}
			repo, err := backends.Open(context.Background(), "pgstac", cfg)
			if err != nil {
				t.Fatalf("open pgstac: %v", err)
			}
			return repo
		},
	}.Run(t)
}
